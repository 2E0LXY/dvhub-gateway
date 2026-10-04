// Package dmr implements the DMR layer-2 pieces needed to build and parse
// voice calls carried over HomeBrew (33-byte burst payloads): full LC
// (RS(12,9) + BPTC(196,96)), embedded LC (Hamming(16,11,4), 5-bit CRC),
// EMB (QR(16,7)), slot type (Golay(20,8)), sync, talker alias and GPS LC.
//
// Bit order is MSB-first throughout, matching ETSI TS 102 361 and MMDVM.
// Verified bit-exact against MMDVMHost (see testdata/dmr_golden.txt).
package dmr

func bit(b []byte, i int) uint8 { return (b[i>>3] >> (7 - i&7)) & 1 }

func setBit(b []byte, i int, v uint8) {
	m := byte(0x80) >> (i & 7)
	if v&1 != 0 {
		b[i>>3] |= m
	} else {
		b[i>>3] &^= m
	}
}

// Burst is one 264-bit DMR burst payload.
type Burst [33]byte
