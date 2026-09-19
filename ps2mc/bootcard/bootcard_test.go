package bootcard_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cwood/ps2mc/ps2mc"
	"github.com/cwood/ps2mc/ps2mc/bootcard"
)

// card copies a fixture and returns a writable card.
func card(t *testing.T) (*ps2mc.Card, string) {
	t.Helper()
	dir := os.Getenv("PS2MC_FIXTURES")
	if dir == "" {
		t.Skip("set PS2MC_FIXTURES to a directory of test cards")
	}
	src, err := os.ReadFile(filepath.Join(dir, "test-ecc.ps2"))
	if err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}
	path := filepath.Join(t.TempDir(), "card.ps2")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("copy: %v", err)
	}
	c, err := ps2mc.Open(path, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return c, path
}

func TestIdentifyFMCBStandard(t *testing.T) {
	c, _ := card(t)
	defer c.Close()

	if err := c.Mkdir("SYS-CONF"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := c.WriteFile("SYS-CONF/FREEMCB.CNF", []byte("CNF_version = 1\n")); err != nil {
		t.Fatalf("write cnf: %v", err)
	}
	if err := c.WriteFile("SYS-CONF/FMCB_CFG.ELF", make([]byte, 4096)); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	p, err := bootcard.Identify(c)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if p.Kind != bootcard.FMCB {
		t.Errorf("kind = %s, want FMCB", p.Kind)
	}
	if p.Layout != bootcard.Standard {
		t.Errorf("layout = %s, want standard", p.Layout)
	}
	if p.Config == "" {
		t.Error("config hash not computed")
	}
	if len(p.Markers) != 2 {
		t.Errorf("markers = %v, want 2", p.Markers)
	}
}

// TestIdentifyFlatLayout covers installs that keep configuration in BOOT
// rather than SYS-CONF; a real card in the wild does this.
func TestIdentifyFlatLayout(t *testing.T) {
	c, _ := card(t)
	defer c.Close()

	if err := c.Mkdir("BOOT"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := c.WriteFile("BOOT/FREEMCB.CNF", []byte("CNF_version = 1\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	p, err := bootcard.Identify(c)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if p.Kind != bootcard.FMCB {
		t.Errorf("kind = %s, want FMCB", p.Kind)
	}
	if p.Layout != bootcard.Flat {
		t.Errorf("layout = %s, want flat", p.Layout)
	}
}

func TestIdentifyPS2BBL(t *testing.T) {
	c, _ := card(t)
	defer c.Close()

	if err := c.Mkdir("SYS-CONF"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := c.WriteFile("SYS-CONF/PS2BBL.INI", []byte("[config]\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	p, err := bootcard.Identify(c)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if p.Kind != bootcard.PS2BBL {
		t.Errorf("kind = %s, want PS2BBL", p.Kind)
	}
}

// TestIdentifyUnknown checks a plain card is not misreported as boot media.
func TestIdentifyUnknown(t *testing.T) {
	c, _ := card(t)
	defer c.Close()

	p, err := bootcard.Identify(c)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if p.Kind != bootcard.Unknown {
		t.Errorf("kind = %s, want unknown", p.Kind)
	}
	if p.Used <= 0 {
		t.Error("used bytes not reported")
	}
}
