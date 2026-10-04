// Package golay implements the systematic Golay(23,12) code used by AMBE
// 3600x2400 (D-STAR) and 3600x2450 (DMR/YSF/NXDN/P25 Ph2) voice frames,
// plus the extended Golay(24,12) wrapper used on C0.
//
// Codeword layout matches mbelib: data in bits 22..11, parity in bits 10..0.
package golay

import "math/bits"

// Generator rows; row i covers data bit (22-i). Identical to mbelib golayGenerator.
var gen = [12]uint32{
	0x63a, 0x31d, 0x7b4, 0x3da, 0x1ed, 0x6cc, 0x366, 0x1b3, 0x6e3, 0x54b, 0x49f, 0x475,
}

// syn maps an 11-bit syndrome to the minimum-weight 23-bit error pattern.
var syn [2048]uint32

func parity11(data12 uint32) uint32 {
	var p uint32
	for i := 0; i < 12; i++ {
		if data12&(1<<(11-i)) != 0 {
			p ^= gen[i]
		}
	}
	return p
}

func syndrome(cw uint32) uint32 { return parity11(cw>>11) ^ (cw & 0x7ff) }

func init() {
	var filled [2048]bool
	set := func(e uint32) {
		if s := syndrome(e); !filled[s] {
			filled[s], syn[s] = true, e
		}
	}
	set(0)
	for a := 0; a < 23; a++ {
		set(1 << a)
		for b := a + 1; b < 23; b++ {
			set(1<<a | 1<<b)
			for c := b + 1; c < 23; c++ {
				set(1<<a | 1<<b | 1<<c)
			}
		}
	}
	for _, ok := range filled {
		if !ok {
			panic("golay: syndrome table incomplete") // perfect code: unreachable
		}
	}
}

// Encode23 returns the 23-bit codeword for 12 data bits.
func Encode23(data12 uint16) uint32 {
	d := uint32(data12) & 0xfff
	return d<<11 | parity11(d)
}

// Decode23 corrects up to 3 bit errors. Returns the 12 data bits and the
// weight of the corrected error pattern (0..3, data and parity bits).
func Decode23(cw uint32) (uint16, int) {
	cw &= 0x7fffff
	e := syn[syndrome(cw)]
	return uint16((cw ^ e) >> 11), bits.OnesCount32(e)
}

// DataCorrection returns the 12-bit data correction mask for a syndrome.
// Exposed for cross-checking against mbelib's golayMatrix.
func DataCorrection(s uint16) uint16 { return uint16(syn[s&0x7ff] >> 11) }

// Encode24 returns the extended codeword: 23-bit code in bits 23..1 and
// even overall parity in bit 0 (AMBE C0 places this in ambe_fr[0][0]).
func Encode24(data12 uint16) uint32 {
	cw := Encode23(data12)
	return cw<<1 | uint32(bits.OnesCount32(cw)&1)
}

// Decode24 decodes an extended codeword, correcting up to 3 errors in total
// (parity bit included). parityOK=false only when the error pattern is
// detected but uncorrectable (3 corrections in the 23-bit word plus a parity
// mismatch: typically 4 errors). A lone parity-bit error is corrected.
func Decode24(cw uint32) (data uint16, errs int, parityOK bool) {
	data, errs = Decode23(cw >> 1)
	if Encode24(data)&1 == cw&1 {
		return data, errs, true
	}
	if errs == 3 {
		return data, errs + 1, false
	}
	return data, errs + 1, true
}
