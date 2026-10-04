package dstar

import (
	"math"
	"testing"
)

func TestCRCKnownValue(t *testing.T) {
	if got := CRC([]byte("123456789")); got != 0x906E { // CRC-16/X-25 check value
		t.Fatalf("crc %04x", got)
	}
}

func TestHeaderRoundTrip(t *testing.T) {
	h := Header{RPT2: Field("XLX123", 'G'), RPT1: Field("XLX123", 'B'), YOUR: "CQCQCQ", MY: "2E0LXY", Suffix: "DMR"}
	b := h.Bytes()
	g, ok := ParseHeader(b[:])
	if !ok || g.Callsign() != "2E0LXY" || g.RPT1 != "XLX123 B" {
		t.Fatalf("%v %v", g, ok)
	}
	b[20] ^= 1
	if _, ok := ParseHeader(b[:]); ok {
		t.Fatal("CRC did not detect error")
	}
}

func TestSlowDataRoundTrip(t *testing.T) {
	h := Header{RPT1: "GB7XX  B", RPT2: "GB7XX  G", YOUR: "CQCQCQ", MY: "M0ABC", Suffix: "DMR"}
	pos := Position{53.6833, -1.4977}
	m := NewMuxer("Hello from DMR", &h, DPRS("M0ABC", pos, "via dvxcode"))
	var d Demuxer
	for n := 0; n < 21*6; n++ {
		d.Add(n%21, m.Next(n%21))
	}
	if d.Text != "Hello from DMR" {
		t.Fatalf("text %q", d.Text)
	}
	if d.Header == nil || d.Header.Callsign() != "M0ABC" {
		t.Fatalf("header %+v", d.Header)
	}
	if d.Pos == nil || math.Abs(d.Pos.Lat-pos.Lat) > 1e-3 || math.Abs(d.Pos.Lon-pos.Lon) > 1e-3 {
		t.Fatalf("pos %+v", d.Pos)
	}
}

func TestSyncFrame(t *testing.T) {
	if NewMuxer("", nil, "").Next(0) != SyncBytes {
		t.Fatal("frame 0 must carry sync")
	}
}

func TestParseNMEA(t *testing.T) {
	p, ok := ParsePosition("$GPRMC,123519,A,5340.998,N,00129.862,W,0.0,0.0,040126,,*6A")
	if !ok || math.Abs(p.Lat-53.68330) > 1e-4 || math.Abs(p.Lon+1.4977) > 1e-4 {
		t.Fatalf("%+v %v", p, ok)
	}
}
