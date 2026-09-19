package image

import (
	"bufio"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// TestSpareMatchesReference checks the Hamming code against vectors produced by
// mymc+, the reference implementation. Byte-exactness matters: a card written
// with wrong ECC is silently corrupt on real hardware.
func TestSpareMatchesReference(t *testing.T) {
	path := os.Getenv("ECC_VECTORS")
	if path == "" {
		t.Skip("set ECC_VECTORS to a reference vector file")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open vectors: %v", err)
	}
	defer f.Close()

	n := 0
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for s.Scan() {
		page, spare, ok := strings.Cut(s.Text(), " ")
		if !ok {
			continue
		}
		data, err := hex.DecodeString(page)
		if err != nil {
			t.Fatalf("bad page hex: %v", err)
		}
		want, err := hex.DecodeString(spare)
		if err != nil {
			t.Fatalf("bad spare hex: %v", err)
		}
		got := Spare(data)
		// The reference emits 12 bytes (four 3-byte codes); ours pads to 16.
		if hex.EncodeToString(got[:len(want)]) != hex.EncodeToString(want) {
			t.Fatalf("vector %d mismatch:\n got %x\nwant %x", n, got[:len(want)], want)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no vectors read")
	}
	t.Logf("verified %d vectors", n)
}
