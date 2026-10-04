package mbe

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/2E0LXY/dvxcode/ambe"
)

// Golden: reference/refdump parms <mode> N (random bit sequences, stateful).
func checkGolden(t *testing.T, m ambe.Mode, path string) {
	fh, err := os.Open(path)
	if err != nil {
		t.Skip("reference dump absent:", err)
	}
	defer fh.Close()
	dec := NewDecoder(m)
	s := bufio.NewScanner(fh)
	s.Buffer(make([]byte, 1<<20), 1<<20)
	var n, voice int
	var maxErr float64
	for s.Scan() {
		halves := strings.SplitN(s.Text(), "|", 2)
		f := strings.Fields(halves[0])
		var bits ambe.Bits49
		for i, c := range f[0] {
			bits[i] = uint8(c - '0')
		}
		bad, _ := strconv.Atoi(f[1])
		p, k := dec.Decode(bits)
		gotBad := int(k)
		if k == Silence {
			gotBad = 0
		}
		if gotBad != bad {
			t.Fatalf("frame %d: kind %v, mbelib bad=%d", n, k, bad)
		}
		n++
		if bad != 0 {
			continue
		}
		voice++
		w0, _ := strconv.ParseFloat(f[2], 64)
		L, _ := strconv.Atoi(f[3])
		g, _ := strconv.ParseFloat(f[4], 64)
		if L != p.L || math.Abs(w0-float64(p.W0)) > 1e-5 || math.Abs(g-float64(p.Gamma)) > 1e-3 {
			t.Fatalf("frame %d: w0 %v/%v L %d/%d gamma %v/%v", n, p.W0, w0, p.L, L, p.Gamma, g)
		}
		vuv := f[5]
		for l := 1; l <= L; l++ {
			if int8(vuv[l-1]-'0') != p.Vl[l] {
				t.Fatalf("frame %d: Vl[%d]", n, l)
			}
		}
		mags := strings.Fields(halves[1])
		for l := 1; l <= L; l++ {
			v, _ := strconv.ParseFloat(mags[l-1], 64)
			e := math.Abs(v - float64(p.Log2Ml[l]))
			if e > maxErr {
				maxErr = e
			}
			if e > 1e-3 {
				t.Fatalf("frame %d: log2Ml[%d] go %v mbelib %v", n, l, p.Log2Ml[l], v)
			}
		}
	}
	t.Logf("%v: %d frames (%d voice/silence), max |log2Ml| diff %.2e", m, n, voice, maxErr)
}

func TestGoldenDMR(t *testing.T)   { checkGolden(t, ambe.DMR, "../testdata/mbelib_parms_dmr.txt") }
func TestGoldenDStar(t *testing.T) { checkGolden(t, ambe.DStar, "../testdata/mbelib_parms_dstar.txt") }

func BenchmarkDecodeDMR(b *testing.B) {
	dec := NewDecoder(ambe.DMR)
	var bits ambe.Bits49
	for i := 0; i < b.N; i++ {
		bits[i%49] ^= 1
		dec.Decode(bits)
	}
}
