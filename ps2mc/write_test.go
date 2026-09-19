package ps2mc

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwood/ps2mc/ps2mc/image"
)

// TestWriteReadBack installs a file and reads it back, on both page layouts.
func TestWriteReadBack(t *testing.T) {
	for _, name := range []string{"test-ecc.ps2", "test-raw.ps2"} {
		t.Run(name, func(t *testing.T) {
			path := fixture(t, name)
			payload := make([]byte, 7000)
			if _, err := rand.Read(payload); err != nil {
				t.Fatalf("rand: %v", err)
			}

			c, err := Open(path, true)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if err := c.WriteFile("APPS/NEW.ELF", payload); err != nil {
				t.Fatalf("write: %v", err)
			}
			if err := c.Sync(); err != nil {
				t.Fatalf("sync: %v", err)
			}
			c.Close()

			// Reopen so the read goes through a fresh probe, not cached state.
			c, err = Open(path, false)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			defer c.Close()

			got, err := c.ReadFile("APPS/NEW.ELF")
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("content differs: got %d bytes, want %d", len(got), len(payload))
			}
		})
	}
}

// TestWriteReplaceReclaims checks that overwriting a file frees its old
// clusters rather than leaking them.
//
// The measurement starts after the first write: creating a file may also grow
// the parent directory by a cluster, and directories never shrink, so absolute
// free-space figures would fold that one-off cost into the result.
func TestWriteReplaceReclaims(t *testing.T) {
	c, err := Open(fixture(t, "test-ecc.ps2"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()

	big := make([]byte, 20000)
	if err := c.WriteFile("APPS/BIG.BIN", big); err != nil {
		t.Fatalf("write big: %v", err)
	}
	settled, err := c.Free()
	if err != nil {
		t.Fatalf("free: %v", err)
	}

	for i := range 3 {
		if err := c.WriteFile("APPS/BIG.BIN", big); err != nil {
			t.Fatalf("rewrite %d: %v", i, err)
		}
		after, err := c.Free()
		if err != nil {
			t.Fatalf("free: %v", err)
		}
		if after != settled {
			t.Fatalf("rewrite %d changed free space %d -> %d (old chain leaked)", i, settled, after)
		}
	}
}

// TestRemoveFreesSpace checks deletion returns a file's clusters to the pool.
//
// A full write/remove cycle is run once to absorb any one-off directory growth,
// then repeated: from there each cycle must be free-space neutral.
func TestRemoveFreesSpace(t *testing.T) {
	c, err := Open(fixture(t, "test-ecc.ps2"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()

	cycle := func() int64 {
		t.Helper()
		if err := c.WriteFile("APPS/TMP.BIN", make([]byte, 10000)); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := c.Remove("APPS/TMP.BIN"); err != nil {
			t.Fatalf("remove: %v", err)
		}
		free, err := c.Free()
		if err != nil {
			t.Fatalf("free: %v", err)
		}
		return free
	}

	settled := cycle()
	for i := range 3 {
		if got := cycle(); got != settled {
			t.Fatalf("cycle %d: free space %d -> %d, expected neutral", i, settled, got)
		}
	}
	if _, err := c.ReadFile("APPS/TMP.BIN"); err == nil {
		t.Error("removed file still readable")
	}
}

// TestNameTooLong guards the format's 32-character filename limit.
func TestNameTooLong(t *testing.T) {
	c, err := Open(fixture(t, "test-ecc.ps2"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()

	long := "THIS_FILENAME_IS_DEFINITELY_LONGER_THAN_THIRTY_TWO.ELF"
	if err := c.WriteFile("APPS/"+long, []byte("x")); err == nil {
		t.Error("expected an error for an over-long name")
	}
}

// TestConvertRoundTrip converts between layouts and back, and checks the
// filesystem still reads identically.
func TestConvertRoundTrip(t *testing.T) {
	src := fixture(t, "test-ecc.ps2")
	dir := t.TempDir()
	raw := filepath.Join(dir, "as-raw.ps2")
	back := filepath.Join(dir, "as-ecc.ps2")

	c, err := Open(src, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := image.Convert(c.Image(), raw, image.Raw); err != nil {
		t.Fatalf("to raw: %v", err)
	}
	c.Close()

	c, err = Open(raw, false)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if c.Format() != image.Raw {
		t.Errorf("format = %s, want raw", c.Format())
	}
	data, err := c.ReadFile("APPS/HELLO.TXT")
	if err != nil {
		t.Fatalf("read from raw: %v", err)
	}
	if err := image.Convert(c.Image(), back, image.ECC); err != nil {
		t.Fatalf("back to ecc: %v", err)
	}
	c.Close()

	c, err = Open(back, false)
	if err != nil {
		t.Fatalf("open reconverted: %v", err)
	}
	defer c.Close()
	if c.Format() != image.ECC {
		t.Errorf("format = %s, want ecc", c.Format())
	}
	again, err := c.ReadFile("APPS/HELLO.TXT")
	if err != nil {
		t.Fatalf("read after round trip: %v", err)
	}
	if !bytes.Equal(data, again) {
		t.Error("content changed across layout round trip")
	}
}

// TestSnapshotRestore checks a snapshot verifies and restores exactly.
func TestSnapshotRestore(t *testing.T) {
	path := fixture(t, "test-ecc.ps2")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	dir := t.TempDir()

	b, err := Snapshot(path, dir, "unit test")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := b.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Mutate the card, then restore it.
	c, err := Open(path, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := c.WriteFile("APPS/SCRATCH.BIN", make([]byte, 5000)); err != nil {
		t.Fatalf("write: %v", err)
	}
	c.Close()

	if err := b.Restore(path); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if !bytes.Equal(original, restored) {
		t.Error("restored image differs from the original")
	}
}
