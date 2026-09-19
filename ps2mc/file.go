package ps2mc

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"
)

// maxNameLen is the longest filename the card format stores.
const maxNameLen = 32

// ErrNameTooLong is returned for names the format cannot represent.
var ErrNameTooLong = errors.New("ps2mc: name exceeds 32 characters")

// ReadFile returns the contents of a file on the card.
func (c *Card) ReadFile(p string) ([]byte, error) {
	e, err := c.stat(p)
	if err != nil {
		return nil, err
	}
	if e.IsDir() {
		return nil, fmt.Errorf("ps2mc: %s is a directory", p)
	}
	if e.Length == 0 {
		return nil, nil
	}
	clusters, err := c.chain(e.Cluster)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, e.Length)
	for _, cluster := range clusters {
		data, err := c.readData(cluster)
		if err != nil {
			return nil, err
		}
		out = append(out, data...)
		if len(out) >= int(e.Length) {
			break
		}
	}
	return out[:e.Length], nil
}

// WriteFile writes data to a file on the card, replacing it if it already
// exists. The parent directory must exist; intermediate directories are not
// created.
//
// The directory slot is claimed before any data clusters are allocated. Doing
// it the other way round leaks the allocation when no slot is available, which
// silently corrupts the card's free-space accounting.
func (c *Card) WriteFile(p string, data []byte) error {
	if !c.writable {
		return errors.New("ps2mc: card opened read-only")
	}
	dir, name := path.Split(strings.Trim(path.Clean("/"+p), "/"))
	if len(name) > maxNameLen {
		return fmt.Errorf("%w: %q", ErrNameTooLong, name)
	}
	parent, parentLoc, err := c.locate(dir)
	if err != nil {
		return err
	}
	if !parent.IsDir() {
		return fmt.Errorf("ps2mc: %s is not a directory", dir)
	}

	entries, clusters, err := c.readDirEntries(parent.Cluster)
	if err != nil {
		return err
	}

	// Reuse the slot of a same-named file, releasing its old contents.
	slot := -1
	for i, e := range entries {
		if e.Exists() && strings.EqualFold(e.Name, name) {
			if e.IsDir() {
				return fmt.Errorf("ps2mc: %s is a directory", p)
			}
			if e.Length > 0 {
				if err := c.freeChain(e.Cluster); err != nil {
					return err
				}
			}
			slot = i
			break
		}
	}
	if slot < 0 {
		if slot, clusters, err = c.claimSlot(parent, parentLoc); err != nil {
			return err
		}
	}

	first := uint32(fatClusterMask)
	if len(data) > 0 {
		needed := (len(data) + c.clusterSize - 1) / c.clusterSize
		if first, err = c.allocChain(needed); err != nil {
			return err
		}
		if err := c.writeChainData(first, data); err != nil {
			return err
		}
	}

	now := time.Now()
	return c.writeDirEntry(clusters, slot, Entry{
		Name:     name,
		Mode:     modeExists | modeFile | modeRead | modeWrite | modeExecute | 0x0400,
		Length:   uint32(len(data)),
		Cluster:  first,
		Created:  now,
		Modified: now,
	})
}

// writeChainData fills a cluster chain with data, zero padding the tail.
func (c *Card) writeChainData(first uint32, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	clusters, err := c.chain(first)
	if err != nil {
		return err
	}
	for i, cluster := range clusters {
		buf := make([]byte, c.clusterSize)
		copy(buf, data[i*c.clusterSize:])
		if err := c.writeData(cluster, buf); err != nil {
			return err
		}
	}
	return nil
}

// writeDirEntry writes a single entry slot back into a directory's clusters.
func (c *Card) writeDirEntry(clusters []uint32, slot int, e Entry) error {
	perCluster := c.clusterSize / direntSize
	ci, within := slot/perCluster, slot%perCluster
	if ci >= len(clusters) {
		return fmt.Errorf("ps2mc: directory slot %d beyond chain", slot)
	}
	data, err := c.readData(clusters[ci])
	if err != nil {
		return err
	}
	copy(data[within*direntSize:], encodeEntry(e))
	return c.writeData(clusters[ci], data)
}

// Remove deletes a file, releasing its clusters. Directories are not removed.
func (c *Card) Remove(p string) error {
	if !c.writable {
		return errors.New("ps2mc: card opened read-only")
	}
	dir, name := path.Split(strings.Trim(path.Clean("/"+p), "/"))
	parent, err := c.stat(dir)
	if err != nil {
		return err
	}
	entries, clusters, err := c.readDirEntries(parent.Cluster)
	if err != nil {
		return err
	}
	for i, e := range entries {
		if !e.Exists() || !strings.EqualFold(e.Name, name) {
			continue
		}
		if e.IsDir() {
			return fmt.Errorf("ps2mc: %s is a directory", p)
		}
		if e.Length > 0 {
			if err := c.freeChain(e.Cluster); err != nil {
				return err
			}
		}
		e.Mode &^= modeExists
		return c.writeDirEntry(clusters, i, e)
	}
	return fmt.Errorf("ps2mc: %s: %w", p, fs.ErrNotExist)
}
