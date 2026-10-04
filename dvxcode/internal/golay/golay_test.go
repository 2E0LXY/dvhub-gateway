package golay

import (
	"bufio"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

func TestRoundTripAllCodewordsUpTo3Errors(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for d := 0; d < 4096; d++ {
		cw := Encode23(uint16(d))
		for k := 0; k < 20; k++ {
			var e uint32
			n := r.Intn(4)
			for bitsSet := 0; bitsSet < n; {
				b := uint32(1) << r.Intn(23)
				if e&b == 0 {
					e |= b
					bitsSet++
				}
			}
			got, errs := Decode23(cw ^ e)
			if got != uint16(d) || errs != n {
				t.Fatalf("d=%03x e=%06x got=%03x errs=%d want %d", d, e, got, errs, n)
			}
		}
	}
}

func TestEncode24Parity(t *testing.T) {
	for d := 0; d < 4096; d++ {
		cw := Encode24(uint16(d))
		got, errs, ok := Decode24(cw)
		if got != uint16(d) || errs != 0 || !ok {
			t.Fatalf("d=%03x", d)
		}
		if got, errs, ok := Decode24(cw ^ 1); !ok || got != uint16(d) || errs != 1 {
			t.Fatalf("parity-bit error not corrected d=%03x", d)
		}
	}
}

func TestDecode24DetectsAllWeight4(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for n := 0; n < 200000; n++ {
		d := uint16(r.Intn(4096))
		var e uint32
		for bitsSet := 0; bitsSet < 4; {
			b := uint32(1) << r.Intn(24)
			if e&b == 0 {
				e |= b
				bitsSet++
			}
		}
		if _, _, ok := Decode24(Encode24(d) ^ e); ok {
			t.Fatalf("4-bit error %06x undetected", e)
		}
	}
}

// golayMatrix dumped from mbelib ecc_const.h (one decimal per line).
func TestMatchesMbelibGolayMatrix(t *testing.T) {
	f, err := os.Open("../../testdata/mbelib_golaymatrix.txt")
	if err != nil {
		t.Skip("reference dump absent:", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for i := 0; s.Scan(); i++ {
		v, _ := strconv.Atoi(s.Text())
		if got := DataCorrection(uint16(i)); int(got) != v {
			t.Fatalf("syndrome %d: got %03x mbelib %03x", i, got, v)
		}
	}
}
