package dmr

import (
	"math"
	"testing"
)

func TestTalkerAliasRoundTrip(t *testing.T) {
	for _, s := range []string{"M0ABC", "2E0LXY Daren", "G4KLX Jonathan via D-STAR", "A"} {
		var ta TalkerAlias
		var got string
		var ok bool
		for _, l := range TalkerAliasLCs(s) {
			got, ok = ta.Add(l)
		}
		if !ok || got != s {
			t.Fatalf("%q -> %q (%v)", s, got, ok)
		}
	}
}

// Decode with MMDVMHost's algorithm (CDMRTA::decodeTA, format 0).
func TestTalkerAliasMMDVMHostDecoder(t *testing.T) {
	s := "2E0LXY Daren"
	var buf [32]byte
	for i, l := range TalkerAliasLCs(s) {
		copy(buf[7*i:], l[2:9])
	}
	size := int(buf[0]>>1) & 0x1f
	var out []byte
	t1, c := 0, byte(0)
	for i := 0; i < 32 && len(out) < size; i++ {
		for j := 7; j >= 0; j-- {
			c = c<<1 | (buf[i]>>j)&1
			t1++
			if t1 == 7 {
				if i > 0 {
					out = append(out, c&0x7f)
				}
				t1, c = 0, 0
			}
		}
	}
	if string(out) != s {
		t.Fatalf("MMDVMHost-style decode %q", out)
	}
}

func TestGPSLC(t *testing.T) {
	lat, lon := 53.6833, -1.4977 // Wakefield-ish
	l := GPSLC(lat, lon, 1)
	la, lo, e, ok := GPS(l)
	if !ok || e != 1 || math.Abs(la-lat) > 2e-5 || math.Abs(lo-lon) > 2e-5 {
		t.Fatalf("%v %v %v", la, lo, e)
	}
	// MMDVMHost logGPSPosition arithmetic.
	loI := int32(uint32(l[2]&1)<<31|uint32(l[3])<<23|uint32(l[4])<<15|uint32(l[5])<<7) >> 7
	if math.Abs(float64(loI)*360/33554432-lon) > 2e-5 {
		t.Fatal("longitude layout")
	}
}
