package dmr

// Sync patterns (48 bits).
var (
	SyncBSVoice = [6]byte{0x75, 0x5F, 0xD7, 0xDF, 0x75, 0xF7}
	SyncBSData  = [6]byte{0xDF, 0xF5, 0x7D, 0x75, 0xDF, 0x5D}
	SyncMSVoice = [6]byte{0x7F, 0x7D, 0x5D, 0xD5, 0x7D, 0xFD}
	SyncMSData  = [6]byte{0xD5, 0xD7, 0xF7, 0x7F, 0xD7, 0x57}
)

// SetSync writes a 48-bit sync pattern into burst bits 108..155.
func SetSync(b *Burst, s [6]byte) {
	for i := 0; i < 48; i++ {
		setBit(b[:], 108+i, bit(s[:], i))
	}
}

// SyncDistance returns the Hamming distance between the burst centre and s.
func SyncDistance(b *Burst, s [6]byte) int {
	d := 0
	for i := 0; i < 48; i++ {
		if bit(b[:], 108+i) != bit(s[:], i) {
			d++
		}
	}
	return d
}

// SetSlotType writes colour code and data type (Golay(20,8)) around the sync.
func SetSlotType(b *Burst, cc, dt uint8) {
	cw := golay2087(cc<<4 | dt&0x0f)
	for i := 0; i < 20; i++ {
		v := uint8(cw>>(19-i)) & 1
		if i < 10 {
			setBit(b[:], 98+i, v)
		} else {
			setBit(b[:], 156+i-10, v)
		}
	}
}

// SlotType decodes colour code and data type.
func SlotType(b *Burst) (cc, dt uint8, errs int) {
	var cw uint32
	for i := 0; i < 20; i++ {
		p := 98 + i
		if i >= 10 {
			p = 156 + i - 10
		}
		cw = cw<<1 | uint32(bit(b[:], p))
	}
	v, e := golay2087Decode(cw)
	return v >> 4, v & 0x0f, e
}

// SetEMB writes the EMB (colour code, PI, LCSS) and a 32-bit embedded
// signalling fragment into a voice burst B..F.
func SetEMB(b *Burst, cc uint8, pi bool, lcss uint8, frag [4]byte) {
	v := cc << 3 & 0x78
	if pi {
		v |= 0x04
	}
	v |= lcss & 3
	cw := qrEncode(v)
	for i := 0; i < 16; i++ {
		x := uint8(cw>>(15-i)) & 1
		if i < 8 {
			setBit(b[:], 108+i, x)
		} else {
			setBit(b[:], 148+i-8, x)
		}
	}
	for i := 0; i < 32; i++ {
		setBit(b[:], 116+i, bit(frag[:], i))
	}
}

// EMB decodes the EMB and returns the embedded fragment.
func EMB(b *Burst) (cc uint8, pi bool, lcss uint8, frag [4]byte, errs int) {
	var cw uint16
	for i := 0; i < 16; i++ {
		p := 108 + i
		if i >= 8 {
			p = 148 + i - 8
		}
		cw = cw<<1 | uint16(bit(b[:], p))
	}
	v, e := qrDecode(cw)
	for i := 0; i < 32; i++ {
		setBit(frag[:], i, bit(b[:], 116+i))
	}
	return v >> 3, v&4 != 0, v & 3, frag, e
}

// AMBE frame placement: A bits 0-71, B 72-107 + 156-191, C 192-263.

// SetAMBE writes three 9-byte AMBE frames into the burst voice area.
func SetAMBE(b *Burst, f [3][9]byte) {
	cp := func(di int, src []byte, si, n int) {
		for k := 0; k < n; k++ {
			setBit(b[:], di+k, bit(src, si+k))
		}
	}
	cp(0, f[0][:], 0, 72)
	cp(72, f[1][:], 0, 36)
	cp(156, f[1][:], 36, 36)
	cp(192, f[2][:], 0, 72)
}

// AMBE extracts the three AMBE frames.
func AMBE(b *Burst) (f [3][9]byte) {
	cp := func(dst []byte, di, si, n int) {
		for k := 0; k < n; k++ {
			setBit(dst, di+k, bit(b[:], si+k))
		}
	}
	cp(f[0][:], 0, 0, 72)
	cp(f[1][:], 0, 72, 36)
	cp(f[1][:], 36, 156, 36)
	cp(f[2][:], 0, 192, 72)
	return
}
