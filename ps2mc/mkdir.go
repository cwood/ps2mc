package ps2mc

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// dirMode is the mode a directory entry carries. 0x0400 is set by the console
// on entries it creates; matching it keeps our directories indistinguishable
// from native ones.
const dirMode = modeExists | modeDir | modeRead | modeWrite | modeExecute | 0x0400

// ErrExists is returned when a path is already taken.
var ErrExists = errors.New("ps2mc: already exists")

// Mkdir creates a directory. The parent must already exist.
//
// A new directory occupies one cluster holding its "." and ".." slots. The "."
// slot is not self-referential: its Cluster and ParentEntry point back at the
// entry describing this directory in the parent, which is how the console
// walks upwards.
func (c *Card) Mkdir(p string) error {
	if !c.writable {
		return errors.New("ps2mc: card opened read-only")
	}
	parentPath, name := path.Split(strings.Trim(path.Clean("/"+p), "/"))
	if name == "" {
		return errors.New("ps2mc: refusing to create the root directory")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("%w: %q", ErrNameTooLong, name)
	}

	parent, parentLoc, err := c.locate(parentPath)
	if err != nil {
		return err
	}
	if !parent.IsDir() {
		return fmt.Errorf("ps2mc: %s is not a directory", parentPath)
	}
	if _, err := c.stat(p); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, p)
	}

	slot, chain, err := c.claimSlot(parent, parentLoc)
	if err != nil {
		return err
	}
	cluster, err := c.allocChain(1)
	if err != nil {
		return err
	}

	now := time.Now()
	block := make([]byte, c.clusterSize)
	copy(block, encodeEntry(Entry{
		Name: ".", Mode: dirMode, Created: now, Modified: now,
		Cluster: parent.Cluster, ParentEntry: uint32(slot),
	}))
	copy(block[direntSize:], encodeEntry(Entry{
		Name: "..", Mode: dirMode, Created: now, Modified: now,
	}))
	if err := c.writeData(cluster, block); err != nil {
		return err
	}

	// The child's entry count lives here, in the parent's slot.
	return c.writeDirEntry(chain, slot, Entry{
		Name: name, Mode: dirMode, Length: 2, Cluster: cluster,
		Created: now, Modified: now,
	})
}

// MkdirAll creates a directory and any missing parents.
func (c *Card) MkdirAll(p string) error {
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" {
		return nil
	}
	var built string
	for _, part := range strings.Split(p, "/") {
		built = path.Join(built, part)
		if _, err := c.stat(built); err == nil {
			continue
		}
		if err := c.Mkdir(built); err != nil && !errors.Is(err, ErrExists) {
			return err
		}
	}
	return nil
}

// RemoveDir deletes an empty directory.
//
// Only empty directories are removed: recursive deletion is left to the
// caller, so a mistaken path cannot take a subtree with it.
func (c *Card) RemoveDir(p string) error {
	if !c.writable {
		return errors.New("ps2mc: card opened read-only")
	}
	clean := strings.Trim(path.Clean("/"+p), "/")
	if clean == "" {
		return errors.New("ps2mc: refusing to remove the root directory")
	}
	e, loc, err := c.locate(clean)
	if err != nil {
		return err
	}
	if !e.IsDir() {
		return fmt.Errorf("ps2mc: %s is not a directory", p)
	}
	children, err := c.ReadDir(clean)
	if err != nil {
		return err
	}
	if len(children) > 0 {
		return fmt.Errorf("ps2mc: %s is not empty (%d entries)", p, len(children))
	}
	if err := c.freeChain(e.Cluster); err != nil {
		return err
	}
	e.Mode &^= modeExists
	return c.writeAt(loc, e)
}
