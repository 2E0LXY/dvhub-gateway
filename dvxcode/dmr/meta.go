package dmr

import (
	"math"
	"strings"
)

// ---- Talker alias (ETSI TS 102 361-2 §7.2.18), 7-bit format ----

// TalkerAliasLCs encodes text (ASCII, ≤31 chars) as 1..4 LCs: header and
// blocks 1..3, using the 7-bit format.
func TalkerAliasLCs(text string) []LC {
	var sb strings.Builder
	for _, r := range text {
		if r < 0x20 || r > 0x7e {
			r = '?'
		}
		sb.WriteRune(r)
	}
	t := sb.String()
	if len(t) > 31 {
		t = t[:31]
	}
	nbits := 7 + 7*len(t)
	nlc := (nbits + 55) / 56
	buf := make([]byte, 7*4)
	put := func(pos int, v uint32, n int) {
		for i := 0; i < n; i++ {
			setBit(buf, pos+i, uint8(v>>(n-1-i))&1)
		}
	}
	put(0, 0, 2) // format 0 = 7-bit
	put(2, uint32(len(t)), 5)
	for i := 0; i < len(t); i++ {
		put(7+7*i, uint32(t[i]), 7)
	}
	out := make([]LC, nlc)
	for i := range out {
		out[i][0] = FLCOTAHeader + uint8(i)
		copy(out[i][2:], buf[7*i:7*i+7])
	}
	return out
}

// TalkerAlias accumulates TA LCs and decodes when complete.
type TalkerAlias struct {
	buf  [28]byte
	have uint8
}

// Reset clears accumulated blocks.
func (t *TalkerAlias) Reset() { *t = TalkerAlias{} }

// Add stores a TA LC (FLCO 4..7). Returns the alias once all needed blocks
// are present.
func (t *TalkerAlias) Add(l LC) (string, bool) {
	n := int(l.FLCO()) - FLCOTAHeader
	if n < 0 || n > 3 {
		return "", false
	}
	copy(t.buf[7*n:], l[2:9])
	t.have |= 1 << n
	if t.have&1 == 0 {
		return "", false
	}
	format := t.buf[0] >> 6
	size := int(t.buf[0]>>1) & 0x1f
	var need int
	switch format {
	case 0:
		need = (7 + 7*size + 55) / 56
	case 3:
		need = (8 + 16*size + 55) / 56
	default:
		need = (8 + 8*size + 55) / 56
	}
	if need < 1 {
		need = 1
	}
	if need > 4 || t.have&(1<<need-1) != 1<<need-1 {
		return "", false
	}
	var sb strings.Builder
	switch format {
	case 0:
		for i := 0; i < size; i++ {
			var c byte
			for k := 0; k < 7; k++ {
				c = c<<1 | bit(t.buf[:], 7+7*i+k)
			}
			sb.WriteByte(c)
		}
	case 1, 2:
		for i := 0; i < size && 1+i < len(t.buf); i++ {
			sb.WriteByte(t.buf[1+i])
		}
	case 3:
		for i := 0; i < size && 2+2*i < len(t.buf); i++ {
			if t.buf[1+2*i] == 0 {
				sb.WriteByte(t.buf[2+2*i])
			} else {
				sb.WriteByte('?')
			}
		}
	}
	return strings.TrimSpace(sb.String()), true
}

// ---- GPS info LC (§7.2.16) ----

// GPSLC encodes a position; posErr 0..7 (0 = <2 m, 7 = unknown).
func GPSLC(lat, lon float64, posErr uint8) LC {
	var l LC
	l[0] = FLCOGPSInfo
	lo := int32(math.Round(lon * (1 << 25) / 360))
	la := int32(math.Round(lat * (1 << 24) / 180))
	lo = max(-(1 << 24), min(lo, 1<<24-1))
	la = max(-(1 << 23), min(la, 1<<23-1))
	ulo, ula := uint32(lo)&0x1ffffff, uint32(la)&0xffffff
	l[2] = (posErr&7)<<1 | uint8(ulo>>24)
	l[3], l[4], l[5] = byte(ulo>>16), byte(ulo>>8), byte(ulo)
	l[6], l[7], l[8] = byte(ula>>16), byte(ula>>8), byte(ula)
	return l
}

// GPS decodes a GPS info LC.
func GPS(l LC) (lat, lon float64, posErr uint8, ok bool) {
	if l.FLCO() != FLCOGPSInfo {
		return 0, 0, 0, false
	}
	lo := int32(uint32(l[2]&1)<<31|uint32(l[3])<<23|uint32(l[4])<<15|uint32(l[5])<<7) >> 7
	la := int32(uint32(l[6])<<24|uint32(l[7])<<16|uint32(l[8])<<8) >> 8
	return float64(la) * 180 / (1 << 24), float64(lo) * 360 / (1 << 25), (l[2] >> 1) & 7, true
}
