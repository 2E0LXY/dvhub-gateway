package dmr

// FLCO values (ETSI TS 102 361-2).
const (
	FLCOGroup      = 0x00
	FLCOPrivate    = 0x03
	FLCOTAHeader   = 0x04
	FLCOTABlock1   = 0x05
	FLCOTABlock2   = 0x06
	FLCOTABlock3   = 0x07
	FLCOGPSInfo    = 0x08
	DTVoiceHeader  = 0x01
	DTTerminator   = 0x02
	DTIdle         = 0x09
	lcHeaderMask   = 0x96
	lcTerminatorMk = 0x99
)

// LC is a 72-bit link control word: FLCO, FID, then 7 payload bytes
// (service options + dst + src for voice LCs).
type LC [9]byte

// VoiceLC builds a group or private voice LC.
func VoiceLC(private bool, src, dst uint32) LC {
	var l LC
	if private {
		l[0] = FLCOPrivate
	}
	l[3], l[4], l[5] = byte(dst>>16), byte(dst>>8), byte(dst)
	l[6], l[7], l[8] = byte(src>>16), byte(src>>8), byte(src)
	return l
}

// FLCO returns the opcode (protect/reserved bits masked).
func (l LC) FLCO() uint8 { return l[0] & 0x3f }

// Src returns the source ID of a voice LC.
func (l LC) Src() uint32 { return uint32(l[6])<<16 | uint32(l[7])<<8 | uint32(l[8]) }

// Dst returns the destination ID of a voice LC.
func (l LC) Dst() uint32 { return uint32(l[3])<<16 | uint32(l[4])<<8 | uint32(l[5]) }

// ---- BPTC(196,96) ----

var bptcData = [...][2]int{{4, 11}, {16, 26}, {31, 41}, {46, 56}, {61, 71}, {76, 86}, {91, 101}, {106, 116}, {121, 131}}

func bptcEncode(in [12]byte, b *Burst) {
	var m [196]uint8
	p := 0
	for _, r := range bptcData {
		for a := r[0]; a <= r[1]; a++ {
			m[a] = bit(in[:], p)
			p++
		}
	}
	for r := 0; r < 9; r++ {
		h15113(m[r*15+1 : r*15+16])
	}
	var col [13]uint8
	for c := 0; c < 15; c++ {
		for a := 0; a < 13; a++ {
			col[a] = m[c+1+a*15]
		}
		h1393(col[:])
		for a := 0; a < 13; a++ {
			m[c+1+a*15] = col[a]
		}
	}
	for a := 0; a < 196; a++ {
		setBit(b[:], rawPos((a*181)%196), m[a])
	}
}

// rawPos maps BPTC raw bit index to burst bit index (98 | 98 around the centre).
func rawPos(i int) int {
	if i < 98 {
		return i
	}
	return i + 68
}

func bptcDecode(b *Burst) ([12]byte, bool) {
	var m [196]uint8
	for a := 0; a < 196; a++ {
		m[a] = bit(b[:], rawPos((a*181)%196))
	}
	ok := true
	for pass := 0; pass < 5; pass++ {
		fixing := false
		ok = true
		var col [13]uint8
		for c := 0; c < 15; c++ {
			for a := 0; a < 13; a++ {
				col[a] = m[c+1+a*15]
			}
			f, good := correct1(col[:], h1393)
			if f {
				fixing = true
				for a := 0; a < 13; a++ {
					m[c+1+a*15] = col[a]
				}
			}
			ok = ok && good
		}
		for r := 0; r < 9; r++ {
			f, good := correct1(m[r*15+1:r*15+16], h15113)
			fixing = fixing || f
			ok = ok && good
		}
		if !fixing {
			break
		}
	}
	var out [12]byte
	p := 0
	for _, r := range bptcData {
		for a := r[0]; a <= r[1]; a++ {
			setBit(out[:], p, m[a])
			p++
		}
	}
	return out, ok
}

// EncodeFullLC writes a voice LC header or terminator (dt) into b, including
// slot type for colour code cc and the BS-sourced data sync.
func EncodeFullLC(l LC, dt uint8, cc uint8, b *Burst) {
	var d [12]byte
	copy(d[:9], l[:])
	p := rs129(d[:9])
	mask := byte(lcHeaderMask)
	if dt == DTTerminator {
		mask = lcTerminatorMk
	}
	d[9], d[10], d[11] = p[0]^mask, p[1]^mask, p[2]^mask
	bptcEncode(d, b)
	SetSlotType(b, cc, dt)
	SetSync(b, SyncBSData)
}

// DecodeFullLC extracts and checks the LC in a header/terminator burst.
func DecodeFullLC(b *Burst, dt uint8) (LC, bool) {
	d, ok := bptcDecode(b)
	var l LC
	copy(l[:], d[:9])
	if !ok {
		return l, false
	}
	p := rs129(d[:9])
	mask := byte(lcHeaderMask)
	if dt == DTTerminator {
		mask = lcTerminatorMk
	}
	return l, d[9] == p[0]^mask && d[10] == p[1]^mask && d[11] == p[2]^mask
}

// ---- Embedded LC (voice bursts B..E) ----

// EmbeddedFragments encodes l into four 32-bit fragments (bursts B, C, D, E).
func EmbeddedFragments(l LC) [4][4]byte {
	var crc int
	for _, v := range l {
		crc += int(v)
	}
	crc %= 31
	var d [128]uint8
	for i, pos := range []int{106, 90, 74, 58, 42} {
		d[pos] = uint8(crc>>i) & 1
	}
	b := 0
	for _, r := range [][2]int{{0, 11}, {16, 27}, {32, 42}, {48, 58}, {64, 74}, {80, 90}, {96, 106}} {
		for a := r[0]; a < r[1]; a++ {
			d[a] = bit(l[:], b)
			b++
		}
	}
	for a := 0; a < 112; a += 16 {
		h16114(d[a : a+16])
	}
	for a := 0; a < 16; a++ {
		d[a+112] = d[a] ^ d[a+16] ^ d[a+32] ^ d[a+48] ^ d[a+64] ^ d[a+80] ^ d[a+96]
	}
	var out [4][4]byte
	b = 0
	for a := 0; a < 128; a++ {
		setBit(out[a/32][:], a%32, d[b])
		b += 16
		if b > 127 {
			b -= 127
		}
	}
	return out
}

// DecodeEmbedded reassembles four fragments; ok requires valid Hamming rows
// (single errors corrected), column parity and the 5-bit checksum.
func DecodeEmbedded(fr [4][4]byte) (LC, bool) {
	var d [128]uint8
	b := 0
	for a := 0; a < 128; a++ {
		d[b] = bit(fr[a/32][:], a%32)
		b += 16
		if b > 127 {
			b -= 127
		}
	}
	for a := 0; a < 112; a += 16 {
		if _, ok := correct1(d[a:a+16], h16114); !ok {
			return LC{}, false
		}
	}
	for a := 0; a < 16; a++ {
		if d[a+112] != d[a]^d[a+16]^d[a+32]^d[a+48]^d[a+64]^d[a+80]^d[a+96] {
			return LC{}, false
		}
	}
	var l LC
	b = 0
	for _, r := range [][2]int{{0, 11}, {16, 27}, {32, 42}, {48, 58}, {64, 74}, {80, 90}, {96, 106}} {
		for a := r[0]; a < r[1]; a++ {
			setBit(l[:], b, d[a])
			b++
		}
	}
	crc := 0
	for i, pos := range []int{106, 90, 74, 58, 42} {
		crc |= int(d[pos]) << i
	}
	sum := 0
	for _, v := range l {
		sum += int(v)
	}
	return l, sum%31 == crc
}
