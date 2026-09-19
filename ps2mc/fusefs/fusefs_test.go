//go:build linux || darwin

package fusefs_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/cwood/ps2mc/ps2mc"
	"github.com/cwood/ps2mc/ps2mc/fusefs"
)

// mount serves a copy of a fixture card and returns its mountpoint.
//
// The mountpoint deliberately lives under the user's home rather than the test
// temp directory: fusermount refuses mountpoints it cannot traverse, and
// restrictive temp directories are a common case of that.
func mount(t *testing.T) string {
	t.Helper()

	dir := os.Getenv("PS2MC_FIXTURES")
	if dir == "" {
		t.Skip("set PS2MC_FIXTURES to a directory of test cards")
	}
	if _, err := exec.LookPath("fusermount"); err != nil {
		if _, err := exec.LookPath("fusermount3"); err != nil {
			t.Skip("fusermount not available")
		}
	}
	src, err := os.ReadFile(filepath.Join(dir, "test-ecc.ps2"))
	if err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	base, err := os.MkdirTemp(home, ".ps2mc-test-")
	if err != nil {
		t.Skipf("cannot create mountpoint under home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })

	img := filepath.Join(base, "card.ps2")
	if err := os.WriteFile(img, src, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	mp := filepath.Join(base, "mnt")
	if err := os.Mkdir(mp, 0o755); err != nil {
		t.Fatalf("mkdir mountpoint: %v", err)
	}

	card, err := ps2mc.Open(img, true)
	if err != nil {
		t.Fatalf("open card: %v", err)
	}
	server, err := fusefs.Mount(card, mp, false)
	if err != nil {
		card.Close()
		t.Skipf("mount unavailable here: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Unmount()
		server.Wait()
		card.Close()
	})

	// Wait for the mount to answer before handing it to the test.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(mp, "APPS")); err == nil {
			return mp
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("mount did not become ready")
	return ""
}

func TestReadThroughMount(t *testing.T) {
	mp := mount(t)

	entries, err := os.ReadDir(filepath.Join(mp, "APPS"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	for _, want := range []string{"HELLO.TXT", "BIN.DAT"} {
		if !names[want] {
			t.Errorf("%s missing from mount", want)
		}
	}

	data, err := os.ReadFile(filepath.Join(mp, "APPS", "HELLO.TXT"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello from the reference tool" {
		t.Errorf("content = %q", data)
	}
}

func TestWriteThroughMount(t *testing.T) {
	mp := mount(t)

	payload := []byte("written through the kernel")
	path := filepath.Join(mp, "APPS", "VIAFUSE.TXT")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("content = %q, want %q", got, payload)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != int64(len(payload)) {
		t.Errorf("size = %d, want %d", info.Size(), len(payload))
	}
}

func TestMkdirAndRemoveThroughMount(t *testing.T) {
	mp := mount(t)

	sub := filepath.Join(mp, "TOOLS")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f := filepath.Join(sub, "NOTE.TXT")
	if err := os.WriteFile(f, []byte("note"), 0o644); err != nil {
		t.Fatalf("write in new dir: %v", err)
	}
	if err := os.Remove(f); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if err := os.Remove(sub); err != nil {
		t.Fatalf("rmdir: %v", err)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Error("directory still present after rmdir")
	}
}

// TestNameTooLongThroughMount checks the format's 32-character limit reaches
// userspace as ENAMETOOLONG instead of silently truncating.
func TestNameTooLongThroughMount(t *testing.T) {
	mp := mount(t)

	long := filepath.Join(mp, "APPS", "THIS_FILENAME_IS_LONGER_THAN_THIRTY_TWO_CHARS.ELF")
	err := os.WriteFile(long, []byte("x"), 0o644)
	if err == nil {
		t.Fatal("expected an error for an over-long name")
	}
	if !errors.Is(err, syscall.ENAMETOOLONG) {
		t.Errorf("error = %v, want ENAMETOOLONG", err)
	}
}
