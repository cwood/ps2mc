package image

// PlayStation 2 memory cards protect each 128-byte chunk of a page with a
// 3-byte Hamming code. A 512-byte page therefore carries four codes in its
// 16-byte spare area, the remaining four bytes being unused.

const (
	// chunkSize is the span covered by a single Hamming code.
	chunkSize = 128
	// codeSize is the width of one Hamming code.
	codeSize = 3
)

var (
	parityTable       [256]byte
	columnParityMasks [256]byte
)

func init() {
	cpMasks := [...]byte{0x55, 0x33, 0x0F, 0x00, 0xAA, 0xCC, 0xF0}
	for b := 0; b < 256; b++ {
		parityTable[b] = parity(byte(b))
	}
	for b := 0; b < 256; b++ {
		var mask byte
		for i, m := range cpMasks {
			mask |= parityTable[byte(b)&m] << uint(i)
		}
		columnParityMasks[b] = mask
	}
}

// parity returns 1 when b has an odd number of set bits.
func parity(b byte) byte {
	b ^= b >> 1
	b ^= b >> 2
	b ^= b >> 4
	return b & 1
}

// chunkCode returns the Hamming code protecting a single chunk. Chunks shorter
// than chunkSize are permitted; the trailing bytes simply contribute nothing.
func chunkCode(chunk []byte) [codeSize]byte {
	columnParity := byte(0x77)
	lineParity0 := byte(0x7F)
	lineParity1 := byte(0x7F)
	for i, b := range chunk {
		columnParity ^= columnParityMasks[b]
		if parityTable[b] != 0 {
			lineParity0 ^= ^byte(i)
			lineParity1 ^= byte(i)
		}
	}
	return [codeSize]byte{columnParity, lineParity0 & 0x7F, lineParity1}
}

// Spare returns the 16-byte spare area for a page: one Hamming code per
// 128-byte chunk, zero padded.
func Spare(page []byte) []byte {
	spare := make([]byte, SpareSize)
	for i := 0; i*chunkSize < len(page); i++ {
		start := i * chunkSize
		end := min(start+chunkSize, len(page))
		code := chunkCode(page[start:end])
		copy(spare[i*codeSize:], code[:])
	}
	return spare
}
