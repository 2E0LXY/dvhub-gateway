// Package ambe handles the 72-bit channel frames shared by AMBE 3600x2400
// (D-STAR) and AMBE+2 3600x2450 (DMR, YSF V/D, NXDN, P25 Ph2): interleave,
// PN scrambling and Golay FEC, yielding/consuming 49 parameter bits.
//
// Interleave schedules derive from DSD (ISC licence, see NOTICE).
package ambe

import "github.com/2E0LXY/dvxcode/internal/golay"

// Mode selects the over-the-wire frame flavour.
type Mode int

const (
	DStar Mode = iota // 9 network bytes, LSB-first, AMBE 3600x2400
	DMR               // 9 bytes, MSB-first dibits, AMBE+2 3600x2450
)

func (m Mode) String() string {
	if m == DStar {
		return "dstar"
	}
	return "dmr"
}

// Codeword is the deinterleaved frame: C0 24 bits, C1 23, C2 11, C3 14.
type Codeword [4][24]uint8

// Bits49 holds the 49 vocoder parameter bits in mbelib ambe_d order.
type Bits49 [49]uint8

var cwLen = [4]int{24, 23, 11, 14}

// D-STAR: bit i of the 72-bit stream -> Codeword[dW[i]][dX[i]].
var dW = [72]uint8{
	0, 0, 3, 2, 1, 1, 0, 0, 1, 1, 0, 0, 3, 2, 1, 1, 3, 2, 1, 1, 0, 0, 3, 2,
	0, 0, 3, 2, 1, 1, 0, 0, 1, 1, 0, 0, 3, 2, 1, 1, 3, 2, 1, 1, 0, 0, 3, 2,
	0, 0, 3, 2, 1, 1, 0, 0, 1, 1, 0, 0, 3, 2, 1, 1, 3, 3, 2, 1, 0, 0, 3, 3,
}
var dX = [72]uint8{
	10, 22, 11, 9, 10, 22, 11, 23, 8, 20, 9, 21, 10, 8, 9, 21, 8, 6, 7, 19, 8, 20, 9, 7,
	6, 18, 7, 5, 6, 18, 7, 19, 4, 16, 5, 17, 6, 4, 5, 17, 4, 2, 3, 15, 4, 16, 5, 3,
	2, 14, 3, 1, 2, 14, 3, 15, 0, 12, 1, 13, 2, 0, 1, 13, 0, 12, 10, 11, 0, 12, 1, 13,
}

// DMR: dibit i (bits 2i,2i+1) -> [rW][rX] (MSB of dibit), [rY][rZ] (LSB).
var rW = [36]uint8{0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 2, 0, 2, 0, 2, 0, 2, 0, 2, 0, 2, 0, 2}
var rX = [36]uint8{23, 10, 22, 9, 21, 8, 20, 7, 19, 6, 18, 5, 17, 4, 16, 3, 15, 2, 14, 1, 13, 0, 12, 10, 11, 9, 10, 8, 9, 7, 8, 6, 7, 5, 6, 4}
var rY = [36]uint8{0, 2, 0, 2, 0, 2, 0, 2, 0, 3, 0, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3}
var rZ = [36]uint8{5, 3, 4, 2, 3, 1, 2, 0, 1, 13, 0, 12, 22, 11, 21, 10, 20, 9, 19, 8, 18, 7, 17, 6, 16, 5, 15, 4, 14, 3, 13, 2, 12, 1, 11, 0}

func getBit(b []byte, i int, lsbFirst bool) uint8 {
	if lsbFirst {
		return (b[i>>3] >> (i & 7)) & 1
	}
	return (b[i>>3] >> (7 - i&7)) & 1
}

func setBit(b []byte, i int, v uint8, lsbFirst bool) {
	var m byte
	if lsbFirst {
		m = 1 << (i & 7)
	} else {
		m = 0x80 >> (i & 7)
	}
	if v != 0 {
		b[i>>3] |= m
	} else {
		b[i>>3] &^= m
	}
}

// Deinterleave maps 9 network bytes to the codeword matrix.
func Deinterleave(m Mode, f [9]byte) (c Codeword) {
	if m == DStar {
		for i := 0; i < 72; i++ {
			c[dW[i]][dX[i]] = getBit(f[:], i, true)
		}
		return
	}
	for i := 0; i < 36; i++ {
		c[rW[i]][rX[i]] = getBit(f[:], 2*i, false)
		c[rY[i]][rZ[i]] = getBit(f[:], 2*i+1, false)
	}
	return
}

// Interleave is the inverse of Deinterleave.
func Interleave(m Mode, c Codeword) (f [9]byte) {
	if m == DStar {
		for i := 0; i < 72; i++ {
			setBit(f[:], i, c[dW[i]][dX[i]], true)
		}
		return
	}
	for i := 0; i < 36; i++ {
		setBit(f[:], 2*i, c[rW[i]][rX[i]], false)
		setBit(f[:], 2*i+1, c[rY[i]][rZ[i]], false)
	}
	return
}

// pn returns the C1 whitening bits seeded by the 12 C0 data bits
// (mbelib pr[1..], applied to C1 bits 22..0). The 24th bit applies only to
// D-STAR, whose C1 is extended Golay(24,12) with its parity bit carried in
// C2[10] (confirmed against MMDVMHost CAMBEFEC::regenerateDStar/PRNG_TABLE).
func pn(seed12 uint16) (p [24]uint8) {
	pr := uint32(seed12) * 16
	for k := 0; k < 24; k++ {
		pr = (173*pr + 13849) & 0xffff
		p[k] = uint8(pr >> 15)
	}
	return
}

func packBits(c []uint8, hi, lo int) uint32 { // c[hi] is MSB
	var v uint32
	for j := hi; j >= lo; j-- {
		v = v<<1 | uint32(c[j])
	}
	return v
}

func unpackBits(c []uint8, hi, lo int, v uint32) {
	for j := lo; j <= hi; j++ {
		c[j] = uint8(v & 1)
		v >>= 1
	}
}

// FECStats reports corrected error-pattern weights.
type FECStats struct {
	C0, C1   int  // corrected bits per Golay word
	C0Parity bool // C0 extended parity consistent
	C1Parity bool // D-STAR only (C1 is Golay24); always true for DMR
}

// Total returns the total corrected bit count.
func (s FECStats) Total() int { return s.C0 + s.C1 }

// Decode deinterleaves, descrambles and FEC-decodes one frame.
func Decode(m Mode, f [9]byte) (Bits49, FECStats) {
	c := Deinterleave(m, f)
	var st FECStats
	var d Bits49

	c0, e0, ok := golay.Decode24(packBits(c[0][:], 23, 0))
	st.C0, st.C0Parity = e0, ok
	p := pn(c0)
	for k := 0; k < 23; k++ {
		c[1][22-k] ^= p[k]
	}
	var c1 uint16
	if m == DStar {
		c[2][10] ^= p[23]
		cw := packBits(c[1][:], 22, 0)<<1 | uint32(c[2][10])
		var e1 int
		c1, e1, st.C1Parity = golay.Decode24(cw)
		st.C1 = e1
		c[2][10] = 0 // parity, not a parameter bit: Bits49[24] is always 0 for D-STAR
	} else {
		var e1 int
		c1, e1 = golay.Decode23(packBits(c[1][:], 22, 0))
		st.C1, st.C1Parity = e1, true
	}

	for i := 0; i < 12; i++ {
		d[i] = uint8(c0>>(11-i)) & 1
		d[12+i] = uint8(c1>>(11-i)) & 1
	}
	for j := 10; j >= 0; j-- {
		d[24+10-j] = c[2][j]
	}
	for j := 13; j >= 0; j-- {
		d[35+13-j] = c[3][j]
	}
	return d, st
}

// Encode is the inverse of Decode: FEC, PN whitening and interleave.
func Encode(m Mode, d Bits49) [9]byte {
	var c Codeword
	var c0, c1 uint16
	for i := 0; i < 12; i++ {
		c0 = c0<<1 | uint16(d[i]&1)
		c1 = c1<<1 | uint16(d[12+i]&1)
	}
	unpackBits(c[0][:], 23, 0, golay.Encode24(c0))
	unpackBits(c[1][:], 22, 0, golay.Encode23(c1))
	for j := 10; j >= 0; j-- {
		c[2][j] = d[24+10-j] & 1
	}
	p := pn(c0)
	for k := 0; k < 23; k++ {
		c[1][22-k] ^= p[k]
	}
	if m == DStar {
		c[2][10] = uint8(golay.Encode24(c1)&1) ^ p[23]
	}
	for j := 13; j >= 0; j-- {
		c[3][j] = d[35+13-j] & 1
	}
	return Interleave(m, c)
}
