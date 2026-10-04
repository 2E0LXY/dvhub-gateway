package mbe

import (
	"bufio"
	"encoding/hex"
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/2E0LXY/dvxcode/ambe"
)

func TestPackUnpack(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for _, m := range []ambe.Mode{ambe.DMR, ambe.DStar} {
		for n := 0; n < 5000; n++ {
			var d ambe.Bits49
			for i := range d {
				d[i] = uint8(r.Intn(2))
			}
			if m == ambe.DStar {
				d[24] = 0
			}
			if got := Pack(m, Unpack(m, &d)); got != d {
				t.Fatalf("%v pack/unpack mismatch", m)
			}
		}
	}
}

func loadVectors(t testing.TB, path string) [][9]byte {
	f, err := os.Open(path)
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	var out [][9]byte
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		b, err := hex.DecodeString(line)
		if err != nil || len(b) != 9 {
			t.Fatalf("bad vector %q", line)
		}
		var f [9]byte
		copy(f[:], b)
		out = append(out, f)
	}
	return out
}

// Re-encoding decoded real D-STAR parameters with the same codec must be
// nearly idempotent: identical pitch index, near-identical spectrum.
func TestReencodeRealDStarIdempotent(t *testing.T) {
	vec := loadVectors(t, "../testdata/vectors/dstar_real.hex")
	dec := NewDecoder(ambe.DStar)
	enc := NewEncoder(ambe.DStar)
	enc.Opt.PitchHysteresis, enc.Opt.VUVHysteresis = 0, 0
	var voice, sameB0, sameB1, sameAll int
	var sq, nh float64
	for _, f := range vec {
		bits, _ := ambe.Decode(ambe.DStar, f)
		p, k := dec.Decode(bits)
		if k != Voice || enc.IsNull(bits) {
			enc.EncodeSilence()
			continue
		}
		src := Unpack(ambe.DStar, &bits)
		out := enc.Encode(p)
		got := Unpack(ambe.DStar, &out)
		q := enc.Shadow()
		voice++
		if got.B0 == src.B0 {
			sameB0++
		}
		if got.B1 == src.B1 {
			sameB1++
		}
		if got == src {
			sameAll++
		}
		for l := 1; l <= p.L; l++ {
			d := float64(q.Log2Ml[l] - p.Log2Ml[l])
			sq += d * d
			nh++
		}
	}
	rms := 6.02 * math.Sqrt(sq/nh)
	t.Logf("%d voice frames: b0 same %.1f%%, b1 same %.1f%%, all indices same %.1f%%, rms magnitude error %.2f dB",
		voice, 100*float64(sameB0)/float64(voice), 100*float64(sameB1)/float64(voice), 100*float64(sameAll)/float64(voice), rms)
	if float64(sameB0) < 0.99*float64(voice) || rms > 1.0 {
		t.Fatal("re-encode not idempotent enough")
	}
}

func BenchmarkEncodeDMR(b *testing.B) {
	vec := loadVectors(b, "../testdata/vectors/dstar_real.hex")
	dec := NewDecoder(ambe.DStar)
	var ps []Params
	for _, f := range vec {
		bits, _ := ambe.Decode(ambe.DStar, f)
		if p, k := dec.Decode(bits); k == Voice {
			ps = append(ps, p)
		}
	}
	enc := NewEncoder(ambe.DMR)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc.Encode(ps[i%len(ps)])
	}
}
