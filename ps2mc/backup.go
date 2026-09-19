package ps2mc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Backups are whole-image snapshots taken before a card is modified. A boot
// card carries a homebrew installation that is tedious to rebuild, so the cost
// of a few megabytes is trivial next to the cost of losing it.

// Backup describes one snapshot.
type Backup struct {
	Path    string    `json:"path"`
	Source  string    `json:"source"`
	Taken   time.Time `json:"taken"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256"`
	Comment string    `json:"comment,omitempty"`
}

// DefaultBackupDir is where snapshots live when no directory is given.
func DefaultBackupDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "ps2mc", "backups"), nil
}

// Snapshot copies an image to dir, recording a manifest beside it. The snapshot
// is written to a temporary file and renamed, so an interrupted run cannot
// leave a truncated backup that looks complete.
func Snapshot(src, dir, comment string) (Backup, error) {
	if dir == "" {
		var err error
		if dir, err = DefaultBackupDir(); err != nil {
			return Backup{}, err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Backup{}, err
	}

	in, err := os.Open(src)
	if err != nil {
		return Backup{}, err
	}
	defer in.Close()

	name := fmt.Sprintf("%s-%s", filepath.Base(src), time.Now().UTC().Format("20060102T150405Z"))
	final := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, name+".partial-*")
	if err != nil {
		return Backup{}, err
	}
	defer os.Remove(tmp.Name())

	sum := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, sum), in)
	if err != nil {
		tmp.Close()
		return Backup{}, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Backup{}, err
	}
	if err := tmp.Close(); err != nil {
		return Backup{}, err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return Backup{}, err
	}

	b := Backup{
		Path:    final,
		Source:  src,
		Taken:   time.Now().UTC(),
		Size:    size,
		SHA256:  hex.EncodeToString(sum.Sum(nil)),
		Comment: comment,
	}
	manifest, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return Backup{}, err
	}
	if err := os.WriteFile(final+".json", manifest, 0o644); err != nil {
		return Backup{}, err
	}
	return b, nil
}

// Backups lists snapshots in dir, newest first.
func Backups(dir string) ([]Backup, error) {
	if dir == "" {
		var err error
		if dir, err = DefaultBackupDir(); err != nil {
			return nil, err
		}
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]Backup, 0, len(matches))
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var b Backup
		if json.Unmarshal(data, &b) == nil {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Taken.After(out[j].Taken) })
	return out, nil
}

// Verify recomputes a snapshot's checksum and compares it with the manifest.
func (b Backup) Verify() error {
	f, err := os.Open(b.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != b.SHA256 {
		return fmt.Errorf("ps2mc: %s: checksum mismatch (have %s, want %s)", b.Path, got[:16], b.SHA256[:16])
	}
	return nil
}

// Restore copies a verified snapshot back over an image.
func (b Backup) Restore(dst string) error {
	if err := b.Verify(); err != nil {
		return err
	}
	in, err := os.Open(b.Path)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
