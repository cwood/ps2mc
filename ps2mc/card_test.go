package ps2mc

import (
	"os"
	"path/filepath"
	"testing"
)

// fixture copies a test card into the test's temp dir so writes never touch the
// shared fixture.
func fixture(t *testing.T, name string) string {
	t.Helper()
	dir := os.Getenv("PS2MC_FIXTURES")
	if dir == "" {
		t.Skip("set PS2MC_FIXTURES to a directory of test cards")
	}
	src, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Skipf("fixture %s unavailable: %v", name, err)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return dst
}

func TestReadFixture(t *testing.T) {
	for _, name := range []string{"test-ecc.ps2", "test-raw.ps2"} {
		t.Run(name, func(t *testing.T) {
			c, err := Open(fixture(t, name), false)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer c.Close()

			t.Logf("format=%s clusterSize=%d capacity=%d", c.Format(), c.ClusterSize(), c.Capacity())

			entries, err := c.ReadDir("APPS")
			if err != nil {
				t.Fatalf("readdir APPS: %v", err)
			}
			got := map[string]uint32{}
			for _, e := range entries {
				got[e.Name] = e.Length
			}
			if got["HELLO.TXT"] != 29 {
				t.Errorf("HELLO.TXT length = %d, want 29", got["HELLO.TXT"])
			}
			if got["BIN.DAT"] != 3000 {
				t.Errorf("BIN.DAT length = %d, want 3000", got["BIN.DAT"])
			}

			data, err := c.ReadFile("APPS/HELLO.TXT")
			if err != nil {
				t.Fatalf("read HELLO.TXT: %v", err)
			}
			if string(data) != "hello from the reference tool" {
				t.Errorf("content = %q", data)
			}

			free, err := c.Free()
			if err != nil {
				t.Fatalf("free: %v", err)
			}
			t.Logf("free=%d bytes", free)
		})
	}
}
