// Package dstar implements the D-STAR radio header, slow-data multiplexing
// (text, GPS/DPRS, header) and the DExtra network protocol.
package dstar

import (
	"fmt"
	"strings"
)

// Header is the 41-byte D-STAR radio header.
type Header struct {
	Flags  [3]byte
	RPT2   string // 8
	RPT1   string // 8
	YOUR   string // 8
	MY     string // 8
	Suffix string // 4
}

func pad(s string, n int) []byte {
	b := []byte(strings.ToUpper(s))
	out := make([]byte, n)
	for i := range out {
		out[i] = ' '
		if i < len(b) {
			out[i] = b[i]
		}
	}
	return out
}

// CRC computes the D-STAR CCITT checksum (reflected 0x8408, init 0xFFFF,
// inverted), as used for the header and DPRS.
func CRC(b []byte) uint16 {
	crc := uint16(0xffff)
	for _, v := range b {
		crc ^= uint16(v)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// Bytes encodes the header with its CRC (little-endian).
func (h Header) Bytes() [41]byte {
	var b [41]byte
	copy(b[0:3], h.Flags[:])
	copy(b[3:11], pad(h.RPT2, 8))
	copy(b[11:19], pad(h.RPT1, 8))
	copy(b[19:27], pad(h.YOUR, 8))
	copy(b[27:35], pad(h.MY, 8))
	copy(b[35:39], pad(h.Suffix, 4))
	c := CRC(b[:39])
	b[39], b[40] = byte(c), byte(c>>8)
	return b
}

// ParseHeader decodes a header; ok reports a valid CRC.
func ParseHeader(b []byte) (Header, bool) {
	if len(b) < 41 {
		return Header{}, false
	}
	var h Header
	copy(h.Flags[:], b[0:3])
	h.RPT2 = string(b[3:11])
	h.RPT1 = string(b[11:19])
	h.YOUR = string(b[19:27])
	h.MY = string(b[27:35])
	h.Suffix = string(b[35:39])
	c := CRC(b[:39])
	return h, b[39] == byte(c) && b[40] == byte(c>>8)
}

// Callsign returns MY without padding or module letter ("M0ABC  B" -> "M0ABC").
func (h Header) Callsign() string { return BaseCall(h.MY) }

// BaseCall strips padding and the trailing module/suffix letter of an
// 8-character D-STAR callsign field.
func BaseCall(s string) string {
	s = strings.TrimRight(s, " ")
	if len(s) == 8 && s[6] == ' ' {
		s = strings.TrimRight(s[:7], " ")
	}
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	return s
}

// Field returns an 8-char D-STAR callsign field: call padded, module in column 8.
func Field(call string, module byte) string {
	b := pad(call, 8)
	if module != 0 && module != ' ' {
		b[7] = module
	}
	return string(b)
}

func (h Header) String() string {
	return fmt.Sprintf("MY=%q/%q UR=%q RPT1=%q RPT2=%q", h.MY, h.Suffix, h.YOUR, h.RPT1, h.RPT2)
}
