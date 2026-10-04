package ambe

import (
	"bufio"
	"encoding/hex"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Silence frames from MMDVMHost (DStarDefines.h / DMRDefines.h).
var dstarSilence = [9]byte{0x9E, 0x8D, 0x32, 0x88, 0x26, 0x1A, 0x3F, 0x61, 0xE8}
var dmrSilence = [9]byte{0xB9, 0xE8, 0x81, 0x52, 0x61, 0x73, 0x00, 0x2A, 0x6B}

func b0(m Mode, d Bits49) int {
	if m == DStar {
		v := 0
		for i := 0; i < 6; i++ {
			v = v<<1 | int(d[i])
		}
		return v<<1 | int(d[48])
	}
	return int(d[0])<<6 | int(d[1])<<5 | int(d[2])<<4 | int(d[3])<<3 | int(d[37])<<2 | int(d[38])<<1 | int(d[39])
}

func TestSilenceFramesDecodeCleanAndRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		m Mode
		f [9]byte
	}{{DStar, dstarSilence}, {DMR, dmrSilence}} {
		d, st := Decode(tc.m, tc.f)
		if st.Total() != 0 || !st.C0Parity || !st.C1Parity {
			t.Fatalf("%v silence: FEC errors %+v", tc.m, st)
		}
		if got := b0(tc.m, d); got != 124 {
			t.Fatalf("%v silence: b0=%d want 124", tc.m, got)
		}
		if re := Encode(tc.m, d); re != tc.f {
			t.Fatalf("%v silence re-encode %x != %x", tc.m, re, tc.f)
		}
	}
}

func TestRandomRoundTripAndCorrection(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for _, m := range []Mode{DStar, DMR} {
		for n := 0; n < 20000; n++ {
			var d Bits49
			for i := range d {
				d[i] = uint8(r.Intn(2))
			}
			if m == DStar {
				d[24] = 0 // C1 parity position
			}
			f := Encode(m, d)
			got, st := Decode(m, f)
			if got != d || st.Total() != 0 {
				t.Fatalf("%v clean round trip failed", m)
			}
			// Inject one error into a protected C0 position; must correct.
			c := Deinterleave(m, f)
			c[0][r.Intn(23)+1] ^= 1
			got, st = Decode(m, Interleave(m, c))
			if got != d || st.C0 != 1 {
				t.Fatalf("%v C0 single-error correction failed: %+v", m, st)
			}
		}
	}
}

func TestDStarC1ParityBitCorrected(t *testing.T) {
	c := Deinterleave(DStar, dstarSilence)
	c[2][10] ^= 1
	_, st := Decode(DStar, Interleave(DStar, c))
	if !st.C1Parity || st.C1 != 1 {
		t.Fatalf("C1 parity-bit error not corrected: %+v", st)
	}
}

func TestDMRBurstSilence(t *testing.T) {
	burst, _ := hex.DecodeString("B9E881526173002A6BB9E881526000000000000173002A6BB9E881526173002A6B")
	var p [33]byte
	copy(p[:], burst)
	f := ExtractDMR(p)
	for i := range f {
		if f[i] != dmrSilence {
			t.Fatalf("frame %d = %x", i, f[i])
		}
	}
	var q [33]byte
	copy(q[:], burst)
	InsertDMR(&q, f)
	if q != p {
		t.Fatal("insert altered burst")
	}
}

// Golden vectors from mbelib (reference/refdump fec): "<mode> <hex9> <49 bits>"
func TestMatchesMbelibFEC(t *testing.T) {
	fh, err := os.Open("../testdata/mbelib_fec.txt")
	if err != nil {
		t.Skip("reference dump absent:", err)
	}
	defer fh.Close()
	s := bufio.NewScanner(fh)
	n := 0
	for s.Scan() {
		fs := strings.Fields(s.Text())
		m := DStar
		if fs[0] == "dmr" {
			m = DMR
		}
		raw, _ := hex.DecodeString(fs[1])
		var f [9]byte
		copy(f[:], raw)
		d, _ := Decode(m, f)
		var sb strings.Builder
		for _, b := range d {
			sb.WriteString(strconv.Itoa(int(b)))
		}
		want := fs[2]
		if m == DStar { // mbelib passes the C1 parity bit through as d[24]
			want = want[:24] + "0" + want[25:]
		}
		if sb.String() != want {
			t.Fatalf("line %d %s %s:\n go     %s\n mbelib %s", n, fs[0], fs[1], sb.String(), fs[2])
		}
		n++
	}
	if n == 0 {
		t.Fatal("no vectors")
	}
	t.Logf("%d vectors matched", n)
}
