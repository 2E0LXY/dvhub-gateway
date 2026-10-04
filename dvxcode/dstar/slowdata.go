package dstar

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Slow data: 3 bytes per voice frame, 21 frames per superframe. Frame 0 of
// each superframe carries the sync bytes; frames 1..20 carry ten 6-byte
// blocks, XOR-scrambled per 3-byte half.

var (
	SyncBytes = [3]byte{0x55, 0x2D, 0x16}
	scrambler = [3]byte{0x70, 0x4F, 0x93}
	// EndBytes is the AMBE+data of a DExtra end-of-stream frame.
	EndAMBE = [9]byte{0x55, 0xC8, 0x7A, 0, 0, 0, 0, 0, 0}
	EndData = [3]byte{0x25, 0x1A, 0xC6}
)

const (
	typeGPS    = 0x30
	typeText   = 0x40
	typeHeader = 0x50
	filler     = 0x66
)

// Muxer produces slow data for an outgoing stream.
type Muxer struct {
	blocks [][6]byte
	pos    int
	half   [3]byte
	second bool
}

// NewMuxer builds the block cycle: text message, header copy, then any
// DPRS/GPS sentence, repeated.
func NewMuxer(text string, hdr *Header, gps string) *Muxer {
	m := &Muxer{}
	if text != "" {
		t := []byte(fmt.Sprintf("%-20.20s", text))
		for i := 0; i < 4; i++ {
			var b [6]byte
			b[0] = typeText | byte(i)
			copy(b[1:], t[5*i:5*i+5])
			m.blocks = append(m.blocks, b)
		}
	}
	if hdr != nil {
		hb := hdr.Bytes()
		for i := 0; i < 41; i += 5 {
			var b [6]byte
			n := min(5, 41-i)
			b[0] = typeHeader | byte(n)
			for k := 1; k < 6; k++ {
				b[k] = filler
			}
			copy(b[1:], hb[i:i+n])
			m.blocks = append(m.blocks, b)
		}
	}
	for i := 0; i < len(gps); i += 5 {
		var b [6]byte
		n := min(5, len(gps)-i)
		b[0] = typeGPS | byte(n)
		for k := 1; k < 6; k++ {
			b[k] = filler
		}
		copy(b[1:], gps[i:i+n])
		m.blocks = append(m.blocks, b)
	}
	return m
}

// Next returns the 3 slow-data bytes for voice frame seq (0..20).
func (m *Muxer) Next(seq int) [3]byte {
	if seq == 0 {
		m.second = false
		return SyncBytes
	}
	if !m.second {
		var b [6]byte
		if len(m.blocks) == 0 {
			b = [6]byte{filler, filler, filler, filler, filler, filler}
		} else {
			b = m.blocks[m.pos%len(m.blocks)]
			m.pos++
		}
		copy(m.half[:], b[3:])
		m.second = true
		return scramble([3]byte{b[0], b[1], b[2]})
	}
	m.second = false
	return scramble(m.half)
}

func scramble(b [3]byte) [3]byte {
	return [3]byte{b[0] ^ scrambler[0], b[1] ^ scrambler[1], b[2] ^ scrambler[2]}
}

// Demuxer recovers text, header and GPS from incoming slow data.
type Demuxer struct {
	buf      [6]byte
	second   bool
	text     [20]byte
	textHave uint8
	gps      strings.Builder
	header   []byte
	// Outputs.
	Text   string
	Header *Header
	Pos    *Position
}

// Add consumes the slow data of frame seq (0..20).
func (d *Demuxer) Add(seq int, sd [3]byte) {
	if seq == 0 {
		d.second = false
		return
	}
	s := scramble(sd)
	if !d.second {
		copy(d.buf[:3], s[:])
		d.second = true
		return
	}
	copy(d.buf[3:], s[:])
	d.second = false
	b := d.buf
	switch b[0] & 0xF0 {
	case typeText:
		i := int(b[0] & 0x0F)
		if i < 4 {
			for k := 0; k < 5; k++ {
				d.text[5*i+k] = b[1+k] & 0x7F
			}
			d.textHave |= 1 << i
			if d.textHave == 0x0F {
				d.Text = strings.TrimSpace(string(d.text[:]))
			}
		}
	case typeHeader:
		if len(d.header) < 45 {
			d.header = append(d.header, b[1:6]...)
			if len(d.header) >= 41 {
				if h, ok := ParseHeader(d.header[:41]); ok {
					d.Header = &h
				}
			}
		}
	case typeGPS:
		n := int(b[0] & 0x0F)
		for k := 0; k < n && k < 5; k++ {
			c := b[1+k]
			if c == '\r' || c == '\n' {
				if p, ok := ParsePosition(d.gps.String()); ok {
					d.Pos = &p
				}
				d.gps.Reset()
				continue
			}
			if d.gps.Len() < 256 {
				d.gps.WriteByte(c)
			}
		}
	}
}

// Position is a decoded GPS fix.
type Position struct {
	Lat, Lon float64
}

// ParsePosition accepts a DPRS line ($$CRCxxxx,CALL>...:!DDMM.mmN/DDDMM.mmW...)
// or an NMEA $GPRMC/$GPGGA sentence.
func ParsePosition(s string) (Position, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "$$CRC") {
		i := strings.Index(s, ":!")
		if i < 0 {
			i = strings.Index(s, ":=")
		}
		if i < 0 || len(s) < i+2+18 {
			return Position{}, false
		}
		p := s[i+2:]
		lat, ok1 := dm(p[0:7], p[7], 2)
		lon, ok2 := dm(p[9:17], p[17], 3)
		return Position{lat, lon}, ok1 && ok2
	}
	f := strings.Split(s, ",")
	if len(f) >= 7 && (strings.HasSuffix(f[0], "RMC")) && f[2] == "A" {
		lat, ok1 := dm(f[3], firstByte(f[4]), 2)
		lon, ok2 := dm(f[5], firstByte(f[6]), 3)
		return Position{lat, lon}, ok1 && ok2
	}
	if len(f) >= 7 && strings.HasSuffix(f[0], "GGA") && f[6] != "0" {
		lat, ok1 := dm(f[2], firstByte(f[3]), 2)
		lon, ok2 := dm(f[4], firstByte(f[5]), 3)
		return Position{lat, lon}, ok1 && ok2
	}
	return Position{}, false
}

func firstByte(s string) byte {
	if s == "" {
		return 0
	}
	return s[0]
}

// dm parses DDMM.mm / DDDMM.mm with hemisphere.
func dm(v string, hemi byte, degDigits int) (float64, bool) {
	if len(v) < degDigits+2 {
		return 0, false
	}
	d, err1 := strconv.Atoi(v[:degDigits])
	m, err2 := strconv.ParseFloat(v[degDigits:], 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	x := float64(d) + m/60
	switch hemi {
	case 'S', 'W':
		x = -x
	case 'N', 'E':
	default:
		return 0, false
	}
	return x, true
}

// DPRS returns an APRS position line for D-STAR slow data (GPS-A format).
func DPRS(call string, p Position, comment string) string {
	body := fmt.Sprintf("%s>API51,DSTAR*:!%s/%s[%s", strings.ToUpper(call), aprsLat(p.Lat), aprsLon(p.Lon), comment)
	return fmt.Sprintf("$$CRC%04X,%s\r", CRC([]byte(body)), body)
}

func aprsLat(v float64) string {
	h := byte('N')
	if v < 0 {
		h, v = 'S', -v
	}
	d := math.Floor(v)
	return fmt.Sprintf("%02d%05.2f%c", int(d), (v-d)*60, h)
}

func aprsLon(v float64) string {
	h := byte('E')
	if v < 0 {
		h, v = 'W', -v
	}
	d := math.Floor(v)
	return fmt.Sprintf("%03d%05.2f%c", int(d), (v-d)*60, h)
}
