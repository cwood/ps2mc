// Package image handles the on-disk byte layout of PlayStation 2 memory card
// images.
//
// Two layouts exist in the wild and they are not distinguishable by extension:
//
//   - ECC: each 512-byte page is followed by a 16-byte spare holding the page's
//     Hamming codes. This is how physical cards and most emulators store data.
//   - Raw: pages are stored back to back with no spare area. Adapters such as
//     the SD2PSX write this form.
//
// Callers address pages by index and let the format decide where the bytes
// actually live.
package image

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

// Page and spare geometry. These are fixed by the card format.
const (
	PageSize  = 512
	SpareSize = 16
)

// Format describes how pages are laid out within an image file.
type Format int

const (
	// Raw stores pages with no spare area (SD2PSX and similar adapters).
	Raw Format = iota
	// ECC stores a 16-byte spare after every page (physical cards, emulators).
	ECC
)

func (f Format) String() string {
	if f == ECC {
		return "ecc"
	}
	return "raw"
}

// stride is the number of bytes one page occupies on disk.
func (f Format) stride() int64 {
	if f == ECC {
		return PageSize + SpareSize
	}
	return PageSize
}

// ErrUnknownFormat is returned when a file's size matches neither layout.
var ErrUnknownFormat = errors.New("image: size matches neither raw nor ecc layout")

// File is a memory card image opened for page-level access.
type File struct {
	f          *os.File
	format     Format
	pages      int64
	spareValid bool
}

// Open opens a card image with an explicitly chosen layout.
//
// Layout cannot be inferred from file size: images exist whose size divides by
// 528 while their pages are actually 512 bytes apart, so a size-based guess
// silently misreads them. Callers should try each format and keep whichever
// yields a coherent filesystem.
func Open(path string, write bool, format Format) (*File, error) {
	flag := os.O_RDONLY
	if write {
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := st.Size()
	if size < PageSize {
		f.Close()
		return nil, fmt.Errorf("%w: %d bytes", ErrUnknownFormat, size)
	}
	img := &File{f: f, format: format, pages: size / format.stride()}
	if format == ECC {
		img.spareValid, err = img.checkSpare()
		if err != nil {
			f.Close()
			return nil, err
		}
	}
	return img, nil
}

// checkSpare reports whether page 0's spare area holds its correct Hamming
// codes. Some tools write a 528-byte stride but leave the spare zeroed; such an
// image is readable, and we upgrade it to valid ECC as pages are rewritten.
func (i *File) checkSpare() (bool, error) {
	buf := make([]byte, PageSize+SpareSize)
	if _, err := i.f.ReadAt(buf, 0); err != nil {
		return false, fmt.Errorf("image: read page 0: %w", err)
	}
	return bytes.Equal(Spare(buf[:PageSize]), buf[PageSize:]), nil
}

// SpareValid reports whether the image's spare areas carry real ECC. It is
// false for images written with a zeroed spare, and meaningless for Raw.
func (i *File) SpareValid() bool { return i.spareValid }

// Format reports the layout this image uses.
func (i *File) Format() Format { return i.format }

// Pages reports how many pages the image holds.
func (i *File) Pages() int64 { return i.pages }

// Size reports the image size in bytes.
func (i *File) Size() int64 { return i.pages * i.format.stride() }

// ReadPage reads page n into a new slice of PageSize bytes.
func (i *File) ReadPage(n int64) ([]byte, error) {
	if n < 0 || n >= i.pages {
		return nil, fmt.Errorf("image: page %d out of range (have %d)", n, i.pages)
	}
	buf := make([]byte, PageSize)
	if _, err := i.f.ReadAt(buf, n*i.format.stride()); err != nil {
		return nil, fmt.Errorf("image: read page %d: %w", n, err)
	}
	return buf, nil
}

// WritePage writes page n, recomputing the spare area when the image carries
// one. The caller never has to think about ECC.
func (i *File) WritePage(n int64, data []byte) error {
	if n < 0 || n >= i.pages {
		return fmt.Errorf("image: page %d out of range (have %d)", n, i.pages)
	}
	if len(data) != PageSize {
		return fmt.Errorf("image: page must be %d bytes, got %d", PageSize, len(data))
	}
	off := n * i.format.stride()
	if _, err := i.f.WriteAt(data, off); err != nil {
		return fmt.Errorf("image: write page %d: %w", n, err)
	}
	if i.format == ECC {
		if _, err := i.f.WriteAt(Spare(data), off+PageSize); err != nil {
			return fmt.Errorf("image: write spare %d: %w", n, err)
		}
	}
	return nil
}

// Sync flushes buffered writes to disk.
func (i *File) Sync() error { return i.f.Sync() }

// Close releases the underlying file.
func (i *File) Close() error { return i.f.Close() }

// Convert copies every page of src into a new image at path using the given
// format. It is the supported way to move a card between raw and ECC layouts.
func Convert(src *File, path string, to Format) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	w := io.Writer(out)
	for n := int64(0); n < src.pages; n++ {
		page, err := src.ReadPage(n)
		if err != nil {
			return err
		}
		if _, err := w.Write(page); err != nil {
			return err
		}
		if to == ECC {
			if _, err := w.Write(Spare(page)); err != nil {
				return err
			}
		}
	}
	return out.Sync()
}
