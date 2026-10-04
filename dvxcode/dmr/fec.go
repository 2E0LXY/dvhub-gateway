package dmr

import "math/bits"

// ---- Hamming codes (parity equations as in ETSI TS 102 361-1 Annex B) ----

func h15113(d []uint8) {
	d[11] = d[0] ^ d[1] ^ d[2] ^ d[3] ^ d[5] ^ d[7] ^ d[8]
	d[12] = d[1] ^ d[2] ^ d[3] ^ d[4] ^ d[6] ^ d[8] ^ d[9]
	d[13] = d[2] ^ d[3] ^ d[4] ^ d[5] ^ d[7] ^ d[9] ^ d[10]
	d[14] = d[0] ^ d[1] ^ d[2] ^ d[4] ^ d[6] ^ d[7] ^ d[10]
}

func h1393(d []uint8) {
	d[9] = d[0] ^ d[1] ^ d[3] ^ d[5] ^ d[6]
	d[10] = d[0] ^ d[1] ^ d[2] ^ d[4] ^ d[6] ^ d[7]
	d[11] = d[0] ^ d[1] ^ d[2] ^ d[3] ^ d[5] ^ d[7] ^ d[8]
	d[12] = d[0] ^ d[2] ^ d[4] ^ d[5] ^ d[8]
}

func h16114(d []uint8) {
	d[11] = d[0] ^ d[1] ^ d[2] ^ d[3] ^ d[5] ^ d[7] ^ d[8]
	d[12] = d[1] ^ d[2] ^ d[3] ^ d[4] ^ d[6] ^ d[8] ^ d[9]
	d[13] = d[2] ^ d[3] ^ d[4] ^ d[5] ^ d[7] ^ d[9] ^ d[10]
	d[14] = d[0] ^ d[1] ^ d[2] ^ d[4] ^ d[6] ^ d[7] ^ d[10]
	d[15] = d[0] ^ d[2] ^ d[5] ^ d[6] ^ d[8] ^ d[9] ^ d[10]
}

// correct1 fixes a single-bit error in d (length n) for a code whose parity
// is produced by enc. Returns true if a correction was made, false if the
// word was clean or uncorrectable.
func correct1(d []uint8, enc func([]uint8)) (fixed, ok bool) {
	n := len(d)
	tmp := make([]uint8, n)
	copy(tmp, d)
	enc(tmp)
	clean := true
	for i := range d {
		if tmp[i] != d[i] {
			clean = false
			break
		}
	}
	if clean {
		return false, true
	}
	for i := 0; i < n; i++ {
		copy(tmp, d)
		tmp[i] ^= 1
		chk := make([]uint8, n)
		copy(chk, tmp)
		enc(chk)
		same := true
		for j := range chk {
			if chk[j] != tmp[j] {
				same = false
				break
			}
		}
		if same {
			d[i] ^= 1
			return true, true
		}
	}
	return false, false
}

// ---- QR(16,7,6) for EMB; generator rows from ETSI (codeword = data<<9|parity) ----

var qrRows = [7]uint16{0x273, 0x4e5, 0x9c9, 0x11e2, 0x21b7, 0x411e, 0x804f}

func qrEncode(d7 uint8) uint16 {
	var c uint16
	for i := 0; i < 7; i++ {
		if d7>>i&1 != 0 {
			c ^= qrRows[i]
		}
	}
	return c
}

func qrDecode(cw uint16) (uint8, int) {
	best, bd := uint8(0), 17
	for d := 0; d < 128; d++ {
		if dist := bits.OnesCount16(qrEncode(uint8(d)) ^ cw); dist < bd {
			best, bd = uint8(d), dist
		}
	}
	return best, bd
}

// ---- Golay(20,8,7) for slot type: 8 data bits + 12 parity (byte1, byte2 high nibble) ----

var golayRows = [8]uint16{0xb08e, 0xe093, 0x70a9, 0x60dc, 0x7036, 0xd06c, 0x90d9, 0xa03d}

func golay2087(d uint8) uint32 { // 20-bit codeword: d<<12 | parity12
	var c uint16
	for i := 0; i < 8; i++ {
		if d>>i&1 != 0 {
			c ^= golayRows[i]
		}
	}
	// table value: low byte -> codeword byte1, high byte (top nibble) -> byte2
	return uint32(d)<<12 | uint32(c&0xff)<<4 | uint32(c>>12)
}

func golay2087Decode(cw uint32) (uint8, int) {
	best, bd := uint8(0), 21
	for d := 0; d < 256; d++ {
		if dist := bits.OnesCount32(golay2087(uint8(d)) ^ cw); dist < bd {
			best, bd = uint8(d), dist
		}
	}
	return best, bd
}

// ---- Reed-Solomon (12,9) over GF(2^8), poly 0x11D, g = x^3+14x^2+56x+64 ----

var gfExp [512]uint8
var gfLog [256]uint8

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = uint8(x)
		gfLog[x] = uint8(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gmul(a, b uint8) uint8 {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

var rsPoly = [3]uint8{64, 56, 14}

// rs129 returns the 3 parity bytes (p[2], p[1], p[0] order as transmitted).
func rs129(msg []byte) [3]byte {
	var p [3]uint8
	for _, m := range msg[:9] {
		fb := m ^ p[2]
		p[2] = p[1] ^ gmul(rsPoly[2], fb)
		p[1] = p[0] ^ gmul(rsPoly[1], fb)
		p[0] = gmul(rsPoly[0], fb)
	}
	return [3]byte{p[2], p[1], p[0]}
}
