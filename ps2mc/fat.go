package ps2mc

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// The FAT is two levels deep. A cluster number indexes into a FAT cluster; the
// FAT cluster itself is located through an indirect FAT cluster, whose address
// is held in the superblock. Both levels hold 256 little-endian uint32s.

// ErrNoSpace is returned when the card has no free clusters left.
var ErrNoSpace = errors.New("ps2mc: no free clusters")

// fatEntryLocation resolves which FAT cluster holds the entry for cluster n and
// at what index within it.
func (c *Card) fatEntryLocation(n uint32) (fatCluster uint32, index int, err error) {
	indirectIndex := n / entriesPerCluster
	offset := int(n % entriesPerCluster)

	listIndex := indirectIndex / entriesPerCluster
	listOffset := int(indirectIndex % entriesPerCluster)
	if int(listIndex) >= len(c.sb.IndirectFAT) {
		return 0, 0, fmt.Errorf("ps2mc: cluster %d beyond indirect FAT", n)
	}

	indirect, err := c.readCluster(c.sb.IndirectFAT[listIndex])
	if err != nil {
		return 0, 0, err
	}
	return binary.LittleEndian.Uint32(indirect[listOffset*4:]), offset, nil
}

// fatEntry returns the raw FAT entry for cluster n.
func (c *Card) fatEntry(n uint32) (uint32, error) {
	fatCluster, index, err := c.fatEntryLocation(n)
	if err != nil {
		return 0, err
	}
	fat, err := c.readCluster(fatCluster)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(fat[index*4:]), nil
}

// setFATEntry writes the raw FAT entry for cluster n.
func (c *Card) setFATEntry(n, value uint32) error {
	fatCluster, index, err := c.fatEntryLocation(n)
	if err != nil {
		return err
	}
	fat, err := c.readCluster(fatCluster)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(fat[index*4:], value)
	return c.writeCluster(fatCluster, fat)
}

// chain walks the cluster chain starting at first, returning the allocatable
// cluster numbers in order. A cycle yields an error rather than looping.
func (c *Card) chain(first uint32) ([]uint32, error) {
	var out []uint32
	seen := make(map[uint32]bool)
	for n := first; ; {
		if seen[n] {
			return nil, fmt.Errorf("ps2mc: cycle in cluster chain at %d", n)
		}
		seen[n] = true
		out = append(out, n)

		entry, err := c.fatEntry(n)
		if err != nil {
			return nil, err
		}
		if entry&fatAllocated == 0 {
			return nil, fmt.Errorf("ps2mc: cluster %d not allocated", n)
		}
		next := entry & fatClusterMask
		if next == fatClusterMask {
			return out, nil
		}
		n = next
	}
}

// allocatable reports how many clusters the data area holds.
func (c *Card) allocatable() uint32 { return c.sb.AllocEnd }

// freeClusters counts unallocated clusters in the data area.
func (c *Card) freeClusters() (uint32, error) {
	var free uint32
	for n := uint32(0); n < c.allocatable(); n++ {
		entry, err := c.fatEntry(n)
		if err != nil {
			return 0, err
		}
		if entry&fatAllocated == 0 {
			free++
		}
	}
	return free, nil
}

// Free reports the number of free bytes on the card.
func (c *Card) Free() (int64, error) {
	free, err := c.freeClusters()
	if err != nil {
		return 0, err
	}
	return int64(free) * int64(c.clusterSize), nil
}

// allocChain allocates n clusters and links them into a chain, returning the
// first cluster. The chain is written to the FAT before returning, so a failure
// partway leaves no dangling references.
func (c *Card) allocChain(n int) (uint32, error) {
	if n <= 0 {
		return fatClusterMask, nil
	}
	found := make([]uint32, 0, n)
	for cluster := uint32(0); cluster < c.allocatable() && len(found) < n; cluster++ {
		entry, err := c.fatEntry(cluster)
		if err != nil {
			return 0, err
		}
		if entry&fatAllocated == 0 {
			found = append(found, cluster)
		}
	}
	if len(found) < n {
		return 0, fmt.Errorf("%w: need %d, have %d", ErrNoSpace, n, len(found))
	}
	for i, cluster := range found {
		next := uint32(fatClusterMask)
		if i+1 < len(found) {
			next = found[i+1]
		}
		if err := c.setFATEntry(cluster, fatAllocated|next); err != nil {
			return 0, err
		}
	}
	return found[0], nil
}

// freeChain releases every cluster in a chain.
func (c *Card) freeChain(first uint32) error {
	clusters, err := c.chain(first)
	if err != nil {
		return err
	}
	for _, cluster := range clusters {
		if err := c.setFATEntry(cluster, fatChainEnd&^fatAllocated); err != nil {
			return err
		}
	}
	return nil
}
