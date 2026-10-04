// Package mbe dequantises AMBE 3600x2400 (D-STAR) and AMBE+2 3600x2450
// (DMR family) parameter bits into MBE model parameters.
//
// The arithmetic mirrors mbelib's mbe_decodeAmbe2400Parms /
// mbe_decodeAmbe2450Parms (ISC) so results are verifiable against it.
// See KNOWN_LIMITS in the README for where mbelib's D-STAR model is guessed.
package mbe

import (
	"math"

	"github.com/2E0LXY/dvxcode/ambe"
)

// Kind classifies a decoded frame.
type Kind int

const (
	Voice   Kind = 0
	Erasure Kind = 2 // DMR b0 120..123
	Tone    Kind = 3 // DMR b0 126..127; D-STAR (b0&0x7E)==0x7E
	Silence Kind = 4 // DMR b0 124..125 (decoded as unvoiced, low level)
)

func (k Kind) String() string {
	switch k {
	case Voice:
		return "voice"
	case Erasure:
		return "erasure"
	case Tone:
		return "tone"
	case Silence:
		return "silence"
	}
	return "?"
}

// MaxL is the highest harmonic index.
const MaxL = 56

// Params is one 20 ms MBE frame. Index 0 of the arrays is unused, as in mbelib.
type Params struct {
	W0     float32 // fundamental, radians/sample at 8 kHz
	L      int     // number of harmonics
	Vl     [MaxL + 1]int8
	Log2Ml [MaxL + 2]float32 // log2 spectral magnitudes; [MaxL+1] is an interpolation guard (mbelib reads past its array here)
	Gamma  float32           // gain state
}

// F0Hz returns the fundamental in Hz.
func (p *Params) F0Hz() float64 { return float64(p.W0) / (2 * math.Pi) * 8000 }

// Indices are the raw quantiser indices extracted from the 49 bits.
type Indices struct {
	B0, B1, B2, B3, B4, B5, B6, B7, B8 int
}

// Bit positions (MSB first) of each quantiser index within the 49 bits.
var posDMR = [9][]int{
	{0, 1, 2, 3, 37, 38, 39},
	{4, 5, 6, 7, 35},
	{8, 9, 10, 11, 36},
	{12, 13, 14, 15, 16, 17, 18, 19, 40},
	{20, 21, 22, 23, 41, 42, 43},
	{24, 25, 26, 27, 44},
	{28, 29, 30, 45},
	{31, 32, 33, 46},
	{34, 47, 48},
}

// D-STAR per mbelib's layout. Bit 24 is the C1 parity (not a parameter);
// B8 carries 3 bits and an implicit LSB of 0.
var posDStar = [9][]int{
	{0, 1, 2, 3, 4, 5, 48},
	{38, 39, 40, 41},
	{6, 7, 8, 9, 42, 43},
	{10, 11, 12, 13, 14, 15, 16, 44, 45},
	{17, 18, 19, 20, 21, 46, 47},
	{22, 23, 25, 26},
	{27, 28, 29, 30},
	{31, 32, 33, 34},
	{35, 36, 37},
}

func positions(m ambe.Mode) *[9][]int {
	if m == ambe.DStar {
		return &posDStar
	}
	return &posDMR
}

func (ix *Indices) slots() [9]*int {
	return [9]*int{&ix.B0, &ix.B1, &ix.B2, &ix.B3, &ix.B4, &ix.B5, &ix.B6, &ix.B7, &ix.B8}
}

// Unpack extracts quantiser indices from parameter bits.
func Unpack(m ambe.Mode, d *ambe.Bits49) Indices {
	var ix Indices
	for i, slot := range ix.slots() {
		v := 0
		for _, p := range positions(m)[i] {
			v = v<<1 | int(d[p]&1)
		}
		*slot = v
	}
	if m == ambe.DStar {
		ix.B8 <<= 1
	}
	return ix
}

// Pack is the inverse of Unpack.
func Pack(m ambe.Mode, ix Indices) (d ambe.Bits49) {
	if m == ambe.DStar {
		ix.B8 >>= 1
	}
	for i, slot := range ix.slots() {
		pos := positions(m)[i]
		v := *slot
		for j := len(pos) - 1; j >= 0; j-- {
			d[pos[j]] = uint8(v & 1)
			v >>= 1
		}
	}
	return d
}

// IndicesDMR extracts AMBE+2 3600x2450 quantiser indices.
func IndicesDMR(d *ambe.Bits49) Indices { return Unpack(ambe.DMR, d) }

// IndicesDStar extracts AMBE 3600x2400 indices per mbelib's layout.
func IndicesDStar(d *ambe.Bits49) Indices { return Unpack(ambe.DStar, d) }
