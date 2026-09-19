package ps2mc

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// A directory's entry count lives in its own directory entry, which for
// subdirectories sits in the parent and for the root is the "." slot of the
// root cluster itself. Writing to a directory therefore needs to know not just
// the entry but where that entry is stored, so the count can be updated.

// dirloc identifies where an entry's 512-byte slot lives.
type dirloc struct {
	chain []uint32 // cluster chain holding the slot
	slot  int      // index of the slot within that chain
}

// locate resolves a path to its entry and the location of that entry's slot.
func (c *Card) locate(p string) (Entry, dirloc, error) {
	rootChain, err := c.chain(c.sb.RootDirCluster)
	if err != nil {
		return Entry{}, dirloc{}, err
	}
	// The root describes itself in its own "." slot.
	current, loc := c.root(), dirloc{chain: rootChain, slot: 0}
	if dot, _, err := c.readDirEntries(c.sb.RootDirCluster); err == nil && len(dot) > 0 {
		current.Length = dot[0].Length
	}

	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" {
		return current, loc, nil
	}
	for _, part := range strings.Split(p, "/") {
		if !current.IsDir() {
			return Entry{}, dirloc{}, fmt.Errorf("ps2mc: %s is not a directory", current.Name)
		}
		entries, clusters, err := c.readDirEntries(current.Cluster)
		if err != nil {
			return Entry{}, dirloc{}, err
		}
		found := false
		for i, e := range entries {
			if e.Exists() && strings.EqualFold(e.Name, part) {
				current, loc, found = e, dirloc{chain: clusters, slot: i}, true
				break
			}
		}
		if !found {
			return Entry{}, dirloc{}, fmt.Errorf("ps2mc: %s: %w", p, fs.ErrNotExist)
		}
	}
	return current, loc, nil
}

// writeAt stores an entry into the slot a dirloc names.
func (c *Card) writeAt(loc dirloc, e Entry) error {
	return c.writeDirEntry(loc.chain, loc.slot, e)
}

// setLength updates only the length field of an existing entry.
//
// Re-encoding the whole entry would be wrong here. Entry does not model every
// field the format carries -- the attribute word and 28 reserved bytes are not
// represented -- so a full rewrite zeroes them. For the root directory it is
// worse still: its slot is the "." entry, and rewriting it from a synthesized
// Entry replaces "." with a bogus name and destroys the directory.
func (c *Card) setLength(loc dirloc, length uint32) error {
	perCluster := c.clusterSize / direntSize
	ci, within := loc.slot/perCluster, loc.slot%perCluster
	if ci >= len(loc.chain) {
		return fmt.Errorf("ps2mc: directory slot %d beyond chain", loc.slot)
	}
	data, err := c.readData(loc.chain[ci])
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(data[within*direntSize+4:], length)
	return c.writeData(loc.chain[ci], data)
}

// claimSlot finds a slot in dir for a new entry, extending the directory's
// cluster chain when every existing slot is in use. It returns the slot index
// and the (possibly extended) chain.
//
// Slots below the directory's recorded length may be reused once their entry is
// marked deleted; beyond that the directory grows and its length increases.
func (c *Card) claimSlot(dir Entry, loc dirloc) (int, []uint32, error) {
	entries, clusters, err := c.readDirEntries(dir.Cluster)
	if err != nil {
		return 0, nil, err
	}
	limit := min(int(dir.Length), len(entries))
	for i := 2; i < limit; i++ { // slots 0 and 1 are "." and ".."
		if !entries[i].Exists() {
			return i, clusters, nil
		}
	}

	slot := int(dir.Length)
	perCluster := c.clusterSize / direntSize
	if slot >= len(clusters)*perCluster {
		extended, err := c.growChain(clusters)
		if err != nil {
			return 0, nil, err
		}
		clusters = extended
	}

	if err := c.setLength(loc, dir.Length+1); err != nil {
		return 0, nil, err
	}
	return slot, clusters, nil
}

// growChain appends one zeroed cluster to an existing chain.
func (c *Card) growChain(clusters []uint32) ([]uint32, error) {
	next, err := c.allocChain(1)
	if err != nil {
		return nil, err
	}
	blank := make([]byte, c.clusterSize)
	if err := c.writeData(next, blank); err != nil {
		return nil, err
	}
	last := clusters[len(clusters)-1]
	if err := c.setFATEntry(last, fatAllocated|next); err != nil {
		return nil, err
	}
	return append(clusters, next), nil
}
