// Package fusefs exposes a PlayStation 2 memory card image as a FUSE
// filesystem, so a card can be browsed and edited with ordinary tools.
//
// Files are buffered whole in memory and committed when the last handle
// closes. The card format stores a file as a cluster chain whose length is
// recorded in its directory entry, so there is no meaningful way to write a
// file incrementally; buffering is cheap because a card holds at most 64 MiB.
package fusefs

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	gofs "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"ps2mc/ps2mc"
)

// state is shared by every node. The card is a single mutable handle with FAT
// state, so all operations against it are serialised.
type state struct {
	mu       sync.Mutex
	card     *ps2mc.Card
	readOnly bool
}

// dir is a directory node. The mount root is simply the directory at "/".
type dir struct {
	gofs.Inode
	st   *state
	path string
}

// file is a file node.
type file struct {
	gofs.Inode
	st   *state
	path string
}

var (
	_ gofs.NodeLookuper  = (*dir)(nil)
	_ gofs.NodeReaddirer = (*dir)(nil)
	_ gofs.NodeCreater   = (*dir)(nil)
	_ gofs.NodeMkdirer   = (*dir)(nil)
	_ gofs.NodeUnlinker  = (*dir)(nil)
	_ gofs.NodeRmdirer   = (*dir)(nil)
	_ gofs.NodeGetattrer = (*dir)(nil)
	_ gofs.NodeOpener    = (*file)(nil)
	_ gofs.NodeGetattrer = (*file)(nil)
	_ gofs.NodeSetattrer = (*file)(nil)
)

// errno maps a card error onto the closest errno.
func errno(err error) syscall.Errno {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, fs.ErrNotExist):
		return syscall.ENOENT
	case errors.Is(err, ps2mc.ErrExists):
		return syscall.EEXIST
	case errors.Is(err, ps2mc.ErrNameTooLong):
		return syscall.ENAMETOOLONG
	case errors.Is(err, ps2mc.ErrNoSpace):
		return syscall.ENOSPC
	default:
		return syscall.EIO
	}
}

// setAttr fills a fuse attribute block from a card entry.
func setAttr(e ps2mc.Entry, out *fuse.Attr) {
	if e.IsDir() {
		out.Mode = syscall.S_IFDIR | 0o755
	} else {
		out.Mode = syscall.S_IFREG | 0o644
		out.Size = uint64(e.Length)
	}
	if !e.Modified.IsZero() {
		out.Mtime = uint64(e.Modified.Unix())
		out.Ctime = out.Mtime
	}
	if !e.Created.IsZero() {
		out.Atime = uint64(e.Created.Unix())
	}
}

// Lookup resolves one path component.
func (d *dir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*gofs.Inode, syscall.Errno) {
	d.st.mu.Lock()
	defer d.st.mu.Unlock()

	p := path.Join(d.path, name)
	e, err := d.st.card.Stat(p)
	if err != nil {
		return nil, errno(err)
	}
	setAttr(e, &out.Attr)

	if e.IsDir() {
		return d.NewInode(ctx, &dir{st: d.st, path: p},
			gofs.StableAttr{Mode: syscall.S_IFDIR}), 0
	}
	return d.NewInode(ctx, &file{st: d.st, path: p},
		gofs.StableAttr{Mode: syscall.S_IFREG}), 0
}

// Readdir lists a directory.
func (d *dir) Readdir(ctx context.Context) (gofs.DirStream, syscall.Errno) {
	d.st.mu.Lock()
	defer d.st.mu.Unlock()

	entries, err := d.st.card.ReadDir(d.path)
	if err != nil {
		return nil, errno(err)
	}
	out := make([]fuse.DirEntry, 0, len(entries))
	for _, e := range entries {
		mode := uint32(syscall.S_IFREG)
		if e.IsDir() {
			mode = syscall.S_IFDIR
		}
		out = append(out, fuse.DirEntry{Name: e.Name, Mode: mode})
	}
	return gofs.NewListDirStream(out), 0
}

// Getattr reports a directory's attributes.
func (d *dir) Getattr(ctx context.Context, fh gofs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFDIR | 0o755
	return 0
}

// Mkdir creates a subdirectory.
func (d *dir) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*gofs.Inode, syscall.Errno) {
	if d.st.readOnly {
		return nil, syscall.EROFS
	}
	d.st.mu.Lock()
	defer d.st.mu.Unlock()

	p := path.Join(d.path, name)
	if err := d.st.card.Mkdir(p); err != nil {
		return nil, errno(err)
	}
	if err := d.st.card.Sync(); err != nil {
		return nil, syscall.EIO
	}
	out.Attr.Mode = syscall.S_IFDIR | 0o755
	return d.NewInode(ctx, &dir{st: d.st, path: p},
		gofs.StableAttr{Mode: syscall.S_IFDIR}), 0
}

// Unlink removes a file.
func (d *dir) Unlink(ctx context.Context, name string) syscall.Errno {
	if d.st.readOnly {
		return syscall.EROFS
	}
	d.st.mu.Lock()
	defer d.st.mu.Unlock()

	if err := d.st.card.Remove(path.Join(d.path, name)); err != nil {
		return errno(err)
	}
	return errno(d.st.card.Sync())
}

// Rmdir removes an empty directory.
func (d *dir) Rmdir(ctx context.Context, name string) syscall.Errno {
	if d.st.readOnly {
		return syscall.EROFS
	}
	d.st.mu.Lock()
	defer d.st.mu.Unlock()

	if err := d.st.card.RemoveDir(path.Join(d.path, name)); err != nil {
		if strings.Contains(err.Error(), "not empty") {
			return syscall.ENOTEMPTY
		}
		return errno(err)
	}
	return errno(d.st.card.Sync())
}

// Create makes a new file and returns a handle buffering its contents.
func (d *dir) Create(ctx context.Context, name string, flags, mode uint32, out *fuse.EntryOut) (
	*gofs.Inode, gofs.FileHandle, uint32, syscall.Errno) {
	if d.st.readOnly {
		return nil, nil, 0, syscall.EROFS
	}
	if len(name) > 32 {
		return nil, nil, 0, syscall.ENAMETOOLONG
	}
	d.st.mu.Lock()
	defer d.st.mu.Unlock()

	p := path.Join(d.path, name)
	// Reserve the entry now so ENOSPC and name clashes surface at create time.
	if err := d.st.card.WriteFile(p, nil); err != nil {
		return nil, nil, 0, errno(err)
	}
	node := d.NewInode(ctx, &file{st: d.st, path: p},
		gofs.StableAttr{Mode: syscall.S_IFREG})
	out.Attr.Mode = syscall.S_IFREG | 0o644
	return node, &handle{st: d.st, path: p, dirty: true}, fuse.FOPEN_DIRECT_IO, 0
}

// Getattr reports a file's size and timestamps.
func (f *file) Getattr(ctx context.Context, fh gofs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	f.st.mu.Lock()
	defer f.st.mu.Unlock()

	e, err := f.st.card.Stat(f.path)
	if err != nil {
		return errno(err)
	}
	setAttr(e, &out.Attr)
	return 0
}

// Open reads the file into a buffer that reads and writes then operate on.
func (f *file) Open(ctx context.Context, flags uint32) (gofs.FileHandle, uint32, syscall.Errno) {
	f.st.mu.Lock()
	defer f.st.mu.Unlock()

	data, err := f.st.card.ReadFile(f.path)
	if err != nil {
		return nil, 0, errno(err)
	}
	h := &handle{st: f.st, path: f.path, data: data}
	if flags&uint32(syscall.O_TRUNC) != 0 {
		h.data, h.dirty = nil, true
	}
	return h, fuse.FOPEN_DIRECT_IO, 0
}

// Setattr applies metadata changes. Only truncation is meaningful here: the
// card stores no ownership or permission bits, so chmod and chown are accepted
// and ignored rather than failing tools that set them reflexively.
func (f *file) Setattr(ctx context.Context, fh gofs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if f.st.readOnly {
		return syscall.EROFS
	}
	if size, ok := in.GetSize(); ok {
		f.st.mu.Lock()
		data, err := f.st.card.ReadFile(f.path)
		if err != nil {
			f.st.mu.Unlock()
			return errno(err)
		}
		if uint64(len(data)) > size {
			data = data[:size]
		} else if uint64(len(data)) < size {
			grown := make([]byte, size)
			copy(grown, data)
			data = grown
		}
		err = f.st.card.WriteFile(f.path, data)
		if err == nil {
			err = f.st.card.Sync()
		}
		f.st.mu.Unlock()
		if err != nil {
			return errno(err)
		}
	}
	return f.Getattr(ctx, fh, out)
}

// handle buffers one open file.
type handle struct {
	mu    sync.Mutex
	st    *state
	path  string
	data  []byte
	dirty bool
}

var (
	_ gofs.FileReader   = (*handle)(nil)
	_ gofs.FileWriter   = (*handle)(nil)
	_ gofs.FileFlusher  = (*handle)(nil)
	_ gofs.FileReleaser = (*handle)(nil)
)

func (h *handle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if off >= int64(len(h.data)) {
		return fuse.ReadResultData(nil), 0
	}
	return fuse.ReadResultData(h.data[off:]), 0
}

func (h *handle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	if h.st.readOnly {
		return 0, syscall.EROFS
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	end := off + int64(len(data))
	if end > int64(len(h.data)) {
		grown := make([]byte, end)
		copy(grown, h.data)
		h.data = grown
	}
	copy(h.data[off:], data)
	h.dirty = true
	return uint32(len(data)), 0
}

// Flush commits the buffer to the card. It runs on every close of the handle,
// which is where a copy into the mount actually lands on the card.
func (h *handle) Flush(ctx context.Context) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.dirty {
		return 0
	}
	h.st.mu.Lock()
	defer h.st.mu.Unlock()

	if err := h.st.card.WriteFile(h.path, h.data); err != nil {
		return errno(err)
	}
	if err := h.st.card.Sync(); err != nil {
		return syscall.EIO
	}
	h.dirty = false
	return 0
}

func (h *handle) Release(ctx context.Context) syscall.Errno { return 0 }

// Mount serves the card at mountpoint until the filesystem is unmounted.
func Mount(card *ps2mc.Card, mountpoint string, readOnly bool) (*fuse.Server, error) {
	root := &dir{st: &state{card: card, readOnly: readOnly}, path: "/"}
	opts := &gofs.Options{
		MountOptions: fuse.MountOptions{
			FsName: "ps2mc",
			Name:   "ps2mc",
			// Names are capped at 32 bytes by the format, and the card is
			// small, so caching attributes briefly is safe and keeps
			// directory listings responsive.
			Debug: false,
		},
		AttrTimeout:  durationPtr(time.Second),
		EntryTimeout: durationPtr(time.Second),
	}
	if readOnly {
		opts.MountOptions.Options = append(opts.MountOptions.Options, "ro")
	}
	return gofs.Mount(mountpoint, root, opts)
}

func durationPtr(d time.Duration) *time.Duration { return &d }
