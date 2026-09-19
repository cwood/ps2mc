package ps2mc

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"
)

// Directory entries are 512 bytes each, so a 1 KiB cluster holds two. A
// directory's own chain lists its children, with "." and ".." occupying the
// first two slots.
const direntSize = 512

// Mode flags on a directory entry.
const (
	modeRead      = 0x0001
	modeWrite     = 0x0002
	modeExecute   = 0x0004
	modeProtected = 0x0008
	modeFile      = 0x0010
	modeDir       = 0x0020
	modeHidden    = 0x2000
	modeExists    = 0x8000
)

// Entry is one file or directory on the card.
type Entry struct {
	Name     string
	Mode     uint16
	Length   uint32 // bytes for a file, child count for a directory
	Cluster  uint32 // first cluster of the chain
	Created  time.Time
	Modified time.Time

	// ParentEntry is meaningful only in a directory's own "." slot, where it
	// pairs with Cluster to point back at the entry describing this directory
	// in its parent. Everywhere else it is zero.
	ParentEntry uint32
}

// IsDir reports whether the entry is a directory.
func (e Entry) IsDir() bool { return e.Mode&modeDir != 0 }

// Exists reports whether the entry is live rather than a deleted slot.
func (e Entry) Exists() bool { return e.Mode&modeExists != 0 }

// String renders the entry in a form close to ls -l.
func (e Entry) String() string {
	var b strings.Builder
	if e.IsDir() {
		b.WriteByte('d')
	} else {
		b.WriteByte('-')
	}
	for _, f := range []struct {
		bit  uint16
		char byte
	}{{modeRead, 'r'}, {modeWrite, 'w'}, {modeExecute, 'x'}} {
		if e.Mode&f.bit != 0 {
			b.WriteByte(f.char)
		} else {
			b.WriteByte('-')
		}
	}
	return fmt.Sprintf("%s %9d %s %s", b.String(), e.Length,
		e.Modified.Format("2006-01-02 15:04"), e.Name)
}

// cardZone is the offset card timestamps are stored in. The PlayStation 2
// records times in JST regardless of where the console is, so values must be
// shifted on the way in and out.
var cardZone = time.FixedZone("JST", 9*60*60)

// decodeTime reads the card's 8-byte timestamp: padding, seconds, minutes,
// hours, day, month, then a 16-bit year. The result is in UTC.
func decodeTime(b []byte) time.Time {
	year := binary.LittleEndian.Uint16(b[6:])
	if year == 0 {
		return time.Time{}
	}
	return time.Date(int(year), time.Month(b[5]), int(b[4]),
		int(b[3]), int(b[2]), int(b[1]), 0, cardZone).UTC()
}

// encodeTime writes a timestamp in the card's format.
func encodeTime(t time.Time) [8]byte {
	var b [8]byte
	if t.IsZero() {
		return b
	}
	t = t.In(cardZone)
	b[1] = byte(t.Second())
	b[2] = byte(t.Minute())
	b[3] = byte(t.Hour())
	b[4] = byte(t.Day())
	b[5] = byte(t.Month())
	binary.LittleEndian.PutUint16(b[6:], uint16(t.Year()))
	return b
}

// decodeEntry parses one 512-byte directory entry.
func decodeEntry(b []byte) Entry {
	le := binary.LittleEndian
	name := b[64:512]
	if i := strings.IndexByte(string(name), 0); i >= 0 {
		name = name[:i]
	}
	return Entry{
		Mode:        le.Uint16(b[0:]),
		Length:      le.Uint32(b[4:]),
		Created:     decodeTime(b[8:16]),
		Cluster:     le.Uint32(b[16:]),
		ParentEntry: le.Uint32(b[20:]),
		Modified:    decodeTime(b[24:32]),
		Name:        string(name),
	}
}

// encodeEntry serialises an entry into a 512-byte slot.
func encodeEntry(e Entry) []byte {
	b := make([]byte, direntSize)
	le := binary.LittleEndian
	le.PutUint16(b[0:], e.Mode)
	le.PutUint32(b[4:], e.Length)
	created := encodeTime(e.Created)
	copy(b[8:16], created[:])
	le.PutUint32(b[16:], e.Cluster)
	le.PutUint32(b[20:], e.ParentEntry)
	modified := encodeTime(e.Modified)
	copy(b[24:32], modified[:])
	copy(b[64:], e.Name)
	return b
}

// readDirEntries returns every slot in a directory's chain, live or not, along
// with the cluster chain so callers can write entries back in place.
func (c *Card) readDirEntries(first uint32) ([]Entry, []uint32, error) {
	clusters, err := c.chain(first)
	if err != nil {
		return nil, nil, err
	}
	perCluster := c.clusterSize / direntSize
	entries := make([]Entry, 0, len(clusters)*perCluster)
	for _, cluster := range clusters {
		data, err := c.readData(cluster)
		if err != nil {
			return nil, nil, err
		}
		for i := 0; i < perCluster; i++ {
			entries = append(entries, decodeEntry(data[i*direntSize:]))
		}
	}
	return entries, clusters, nil
}

// ReadDir lists the live entries of a directory, excluding "." and "..".
func (c *Card) ReadDir(dir string) ([]Entry, error) {
	e, err := c.stat(dir)
	if err != nil {
		return nil, err
	}
	if !e.IsDir() {
		return nil, fmt.Errorf("ps2mc: %s is not a directory", dir)
	}
	all, _, err := c.readDirEntries(e.Cluster)
	if err != nil {
		return nil, err
	}
	// Only the first Length slots are meaningful. Slots beyond that are
	// uninitialised and decode into nonsense entries.
	limit := min(int(e.Length), len(all))
	out := make([]Entry, 0, limit)
	for _, entry := range all[:limit] {
		if !entry.Exists() || entry.Name == "." || entry.Name == ".." {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

// root returns the entry describing the root directory.
//
// The root has no parent to hold its metadata, so its entry count is read from
// its own "." slot. Without that, Length would be zero and callers could not
// tell where the directory's live entries stop.
func (c *Card) root() Entry {
	e := Entry{Name: "/", Mode: modeDir | modeExists | modeRead | modeWrite | modeExecute,
		Cluster: c.sb.RootDirCluster}
	data, err := c.readData(c.sb.RootDirCluster)
	if err != nil {
		return e
	}
	if dot := decodeEntry(data); dot.IsDir() && dot.Name == "." {
		e.Length = dot.Length
	}
	return e
}

// stat resolves a slash-separated path to its entry.
func (c *Card) stat(p string) (Entry, error) {
	current := c.root()
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" {
		return current, nil
	}
	for _, part := range strings.Split(p, "/") {
		if !current.IsDir() {
			return Entry{}, fmt.Errorf("ps2mc: %s is not a directory", current.Name)
		}
		all, _, err := c.readDirEntries(current.Cluster)
		if err != nil {
			return Entry{}, err
		}
		found := false
		for _, e := range all {
			if e.Exists() && strings.EqualFold(e.Name, part) {
				current, found = e, true
				break
			}
		}
		if !found {
			return Entry{}, fmt.Errorf("ps2mc: %s: %w", p, fs.ErrNotExist)
		}
	}
	return current, nil
}

// Stat returns the entry for a path.
func (c *Card) Stat(p string) (Entry, error) { return c.stat(p) }
