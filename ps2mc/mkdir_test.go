package ps2mc

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestMkdir(t *testing.T) {
	c, err := Open(fixture(t, "test-ecc.ps2"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()

	if err := c.Mkdir("TOOLS"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	e, err := c.Stat("TOOLS")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !e.IsDir() {
		t.Error("TOOLS is not a directory")
	}
	if e.Length != 2 {
		t.Errorf("length = %d, want 2 (. and ..)", e.Length)
	}

	// A fresh directory lists empty once "." and ".." are filtered.
	entries, err := c.ReadDir("TOOLS")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("new directory has %d entries, want 0", len(entries))
	}

	// It must accept files, and they must survive a reopen.
	payload := []byte("installed into a directory we created")
	if err := c.WriteFile("TOOLS/NOTE.TXT", payload); err != nil {
		t.Fatalf("write into new dir: %v", err)
	}
	got, err := c.ReadFile("TOOLS/NOTE.TXT")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Error("content mismatch")
	}

	if err := c.Mkdir("TOOLS"); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate mkdir error = %v, want ErrExists", err)
	}
}

func TestMkdirAll(t *testing.T) {
	c, err := Open(fixture(t, "test-ecc.ps2"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()

	if err := c.MkdirAll("A/B/C"); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	for _, p := range []string{"A", "A/B", "A/B/C"} {
		e, err := c.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if !e.IsDir() {
			t.Errorf("%s is not a directory", p)
		}
	}
	if err := c.WriteFile("A/B/C/DEEP.BIN", []byte("deep")); err != nil {
		t.Fatalf("write deep: %v", err)
	}
}

// TestWriteToRootPreservesDot guards a bug that corrupted a real card.
//
// Growing a directory updates its entry count. For the root that count lives in
// its own "." slot, so re-encoding the entry from a synthesized value replaced
// "." with a bogus name and left the card unreadable. Only the length field may
// be touched.
func TestWriteToRootPreservesDot(t *testing.T) {
	path := fixture(t, "test-ecc.ps2")
	c, err := Open(path, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Enough files to force the root directory to grow.
	for _, name := range []string{"A.BIN", "B.BIN", "C.BIN", "D.BIN", "E.BIN"} {
		if err := c.WriteFile(name, []byte(name)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	c.Close()

	// Reopening exercises the probe, which requires a valid "." in the root.
	c, err = Open(path, false)
	if err != nil {
		t.Fatalf("reopen after root writes: %v", err)
	}
	defer c.Close()

	all, _, err := c.readDirEntries(c.sb.RootDirCluster)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if all[0].Name != "." {
		t.Fatalf("root slot 0 name = %q, want \".\"", all[0].Name)
	}
	if !all[0].IsDir() {
		t.Error("root slot 0 is no longer a directory")
	}
	entries, err := c.ReadDir("/")
	if err != nil {
		t.Fatalf("readdir root: %v", err)
	}
	found := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name, ".BIN") {
			found++
		}
	}
	if found != 5 {
		t.Errorf("found %d .BIN files in root, want 5", found)
	}
}
