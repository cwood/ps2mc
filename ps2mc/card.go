// Package ps2mc reads and writes the filesystem on PlayStation 2 memory card
// images.
//
// A card is a flat array of 512-byte pages grouped into 1 KiB clusters. A
// superblock in page 0 describes the geometry and locates the FAT and the root
// directory. File contents are cluster chains threaded through a two-level
// indirect FAT.
package ps2mc

import (
	"encoding/binary"
	"errors"
	"fmt"

	"ps2mc/ps2mc/image"
)

const (
	// Magic identifies a formatted card. Trailing space is part of it.
	Magic = "Sony PS2 Memory Card Format "

	// entriesPerCluster is how many 32-bit FAT entries fit in one cluster.
	entriesPerCluster = 256

	// fatAllocated marks a FAT entry as belonging to a chain.
	fatAllocated = 0x80000000
	// fatChainEnd terminates an allocated chain.
	fatChainEnd = 0xFFFFFFFF
	// fatClusterMask extracts the cluster number from a FAT entry.
	fatClusterMask = 0x7FFFFFFF
)

// ErrNotFormatted is returned when an image lacks the card magic.
var ErrNotFormatted = errors.New("ps2mc: not a formatted memory card")

// superblock is the card's geometry and layout descriptor.
type superblock struct {
	Version            [12]byte
	PageSize           uint16
	PagesPerCluster    uint16
	PagesPerEraseBlock uint16
	ClustersPerCard    uint32
	AllocOffset        uint32
	AllocEnd           uint32
	RootDirCluster     uint32
	IndirectFAT        [32]uint32
	CardType           int8
	CardFlags          int8
}

// Card is an open memory card image.
type Card struct {
	img         *image.File
	sb          superblock
	clusterSize int
	writable    bool
}

// Open opens a card image for reading, or for reading and writing when write is
// true. The page layout is determined by probing: each candidate stride is
// tried and the one that yields a coherent filesystem wins.
//
// Probing rather than inspecting the file size is deliberate. Images exist
// whose byte length divides by 528 while their pages are really 512 bytes
// apart, so a size-based guess reads plausible-looking garbage.
func Open(path string, write bool) (*Card, error) {
	var firstErr error
	for _, format := range []image.Format{image.ECC, image.Raw} {
		img, err := image.Open(path, write, format)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		c := &Card{img: img, writable: write}
		if err := c.readSuperblock(); err != nil {
			img.Close()
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if c.rootIsCoherent() {
			return c, nil
		}
		img.Close()
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, ErrNotFormatted
}

// rootIsCoherent reports whether the root directory reads back as a real
// directory. Its first slot must be the "." entry, which is the cheapest
// structural signal that the chosen page stride is the right one.
func (c *Card) rootIsCoherent() bool {
	data, err := c.readData(c.sb.RootDirCluster)
	if err != nil {
		return false
	}
	dot := decodeEntry(data)
	return dot.Exists() && dot.IsDir() && dot.Name == "."
}

func (c *Card) readSuperblock() error {
	page, err := c.img.ReadPage(0)
	if err != nil {
		return err
	}
	if string(page[:len(Magic)]) != Magic {
		return ErrNotFormatted
	}
	le := binary.LittleEndian
	copy(c.sb.Version[:], page[28:40])
	c.sb.PageSize = le.Uint16(page[40:])
	c.sb.PagesPerCluster = le.Uint16(page[42:])
	c.sb.PagesPerEraseBlock = le.Uint16(page[44:])
	c.sb.ClustersPerCard = le.Uint32(page[48:])
	c.sb.AllocOffset = le.Uint32(page[52:])
	c.sb.AllocEnd = le.Uint32(page[56:])
	c.sb.RootDirCluster = le.Uint32(page[60:])
	for i := range c.sb.IndirectFAT {
		c.sb.IndirectFAT[i] = le.Uint32(page[80+i*4:])
	}
	c.sb.CardType = int8(page[336])
	c.sb.CardFlags = int8(page[337])

	if c.sb.PageSize != image.PageSize {
		return fmt.Errorf("ps2mc: unsupported page size %d", c.sb.PageSize)
	}
	c.clusterSize = int(c.sb.PageSize) * int(c.sb.PagesPerCluster)
	return nil
}

// Format reports the image's page layout.
func (c *Card) Format() image.Format { return c.img.Format() }

// ClusterSize reports the card's cluster size in bytes.
func (c *Card) ClusterSize() int { return c.clusterSize }

// Capacity reports the card's total size in bytes.
func (c *Card) Capacity() int64 { return int64(c.sb.ClustersPerCard) * int64(c.clusterSize) }

// Close releases the image.
func (c *Card) Close() error { return c.img.Close() }

// Sync flushes pending writes.
func (c *Card) Sync() error { return c.img.Sync() }

// readCluster reads an absolute cluster number.
func (c *Card) readCluster(n uint32) ([]byte, error) {
	buf := make([]byte, 0, c.clusterSize)
	first := int64(n) * int64(c.sb.PagesPerCluster)
	for p := int64(0); p < int64(c.sb.PagesPerCluster); p++ {
		page, err := c.img.ReadPage(first + p)
		if err != nil {
			return nil, err
		}
		buf = append(buf, page...)
	}
	return buf, nil
}

// writeCluster writes an absolute cluster number.
func (c *Card) writeCluster(n uint32, data []byte) error {
	if !c.writable {
		return errors.New("ps2mc: card opened read-only")
	}
	if len(data) != c.clusterSize {
		return fmt.Errorf("ps2mc: cluster must be %d bytes, got %d", c.clusterSize, len(data))
	}
	first := int64(n) * int64(c.sb.PagesPerCluster)
	for p := int64(0); p < int64(c.sb.PagesPerCluster); p++ {
		off := int(p) * image.PageSize
		if err := c.img.WritePage(first+p, data[off:off+image.PageSize]); err != nil {
			return err
		}
	}
	return nil
}

// readData reads a cluster from the allocatable area, where chains are numbered
// relative to AllocOffset.
func (c *Card) readData(n uint32) ([]byte, error) { return c.readCluster(c.sb.AllocOffset + n) }

// writeData writes a cluster in the allocatable area.
func (c *Card) writeData(n uint32, b []byte) error {
	return c.writeCluster(c.sb.AllocOffset+n, b)
}

// Image exposes the underlying image, for callers that need to work with the
// raw page layout such as format conversion.
func (c *Card) Image() *image.File { return c.img }
