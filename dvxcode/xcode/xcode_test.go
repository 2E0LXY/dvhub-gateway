package xcode

import (
	"bufio"
	"encoding/hex"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/2E0LXY/dvxcode/ambe"
	"github.com/2E0LXY/dvxcode/mbe"
)

var (
	dstarNull = [9]byte{0x9E, 0x8D, 0x32, 0x88, 0x26, 0x1A, 0x3F, 0x61, 0xE8}
	dmrNull   = [9]byte{0xB9, 0xE8, 0x81, 0x52, 0x61, 0x73, 0x00, 0x2A, 0x6B}
)

func vectors(t *testing.T) [][9]byte {
	f, err := os.Open("../testdata/vectors/dstar_real.hex")
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	var out [][9]byte
	s := bufio.NewScanner(f)
	for s.Scan() {
		l := s.Text()
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		b, _ := hex.DecodeString(l)
		var fr [9]byte
		copy(fr[:], b)
		out = append(out, fr)
	}
	return out
}

func TestSilenceMapsToSilence(t *testing.T) {
	opt := DefaultOptions
	opt.Lookahead = false
	if got := New(ambe.DStar, ambe.DMR, opt).Frame(dstarNull); got != dmrNull {
		t.Fatalf("D-STAR null -> %x, want DMR silence", got)
	}
	if got := New(ambe.DMR, ambe.DStar, opt).Frame(dmrNull); got != dstarNull {
		t.Fatalf("DMR silence -> %x, want D-STAR null", got)
	}
}

func TestLookaheadDelay(t *testing.T) {
	tc := New(ambe.DStar, ambe.DMR, DefaultOptions)
	vec := vectors(t)
	for i := 0; i < DefaultOptions.LookaheadFrames; i++ {
		if out := tc.Frame(vec[len(vec)/2+i]); out != dmrNull {
			t.Fatalf("start-up output %d = %x, want silence", i, out)
		}
	}
	if out := tc.Frame(vec[len(vec)/2+5]); out == dmrNull {
		t.Fatal("voice not emitted after lookahead delay")
	}
	if n := len(tc.Flush()); n != DefaultOptions.LookaheadFrames {
		t.Fatalf("flush returned %d frames", n)
	}
}

func TestRealDStarToDMR(t *testing.T) {
	vec := vectors(t)
	src := mbe.NewDecoder(ambe.DStar)
	dst := mbe.NewDecoder(ambe.DMR)
	opt := DefaultOptions
	opt.Lookahead = false
	tc := New(ambe.DStar, ambe.DMR, opt)
	var voiced, close int
	for _, f := range vec {
		sb, _ := ambe.Decode(ambe.DStar, f)
		sp, sk := src.Decode(sb)
		out := tc.Frame(f)
		db, st := ambe.Decode(ambe.DMR, out)
		if st.Total() != 0 || !st.C0Parity {
			t.Fatalf("output frame has FEC errors: %+v", st)
		}
		dp, dk := dst.Decode(db)
		if sk == mbe.Voice && dk == mbe.Voice && sp.VoicedFraction(1000) >= 0.5 {
			voiced++
			if math.Abs(math.Log2(sp.F0Hz()/dp.F0Hz()))*1200 < 50 {
				close++
			}
		}
	}
	if voiced < 150 || close != voiced {
		t.Fatalf("pitch preserved on %d/%d voiced frames", close, voiced)
	}
	t.Logf("%d voiced frames, all within 50 cents", voiced)
}

// Destroy C0 of one frame (4 errors: detected, uncorrectable). The output
// must follow its neighbours rather than emit a random pitch.
func TestConcealmentFollowsNeighbours(t *testing.T) {
	vec := vectors(t)
	src := mbe.NewDecoder(ambe.DStar)
	var ps []mbe.Params
	idx := -1
	for i, f := range vec {
		b, _ := ambe.Decode(ambe.DStar, f)
		p, _ := src.Decode(b)
		ps = append(ps, p)
		if idx < 0 && i > 100 && i+1 < len(vec) {
			a, c := ps[i-1], p
			if a.VoicedFraction(1000) == 1 && c.VoicedFraction(1000) == 1 && math.Abs(math.Log2(a.F0Hz()/c.F0Hz())) < 0.05 {
				idx = i
			}
		}
	}
	if idx < 0 {
		t.Skip("no suitable frame")
	}
	bad := append([][9]byte(nil), vec...)
	c := ambe.Deinterleave(ambe.DStar, bad[idx])
	for _, x := range []int{2, 7, 13, 19} {
		c[0][x] ^= 1
	}
	bad[idx] = ambe.Interleave(ambe.DStar, c)
	if _, st := ambe.Decode(ambe.DStar, bad[idx]); st.C0Parity {
		t.Skip("corruption not detected for this frame")
	}
	tc := New(ambe.DStar, ambe.DMR, DefaultOptions)
	dst := mbe.NewDecoder(ambe.DMR)
	var outF []float64
	for _, f := range bad {
		b, _ := ambe.Decode(ambe.DMR, tc.Frame(f))
		p, _ := dst.Decode(b)
		outF = append(outF, p.F0Hz())
	}
	got := outF[idx+DefaultOptions.LookaheadFrames] // lookahead delay
	want := math.Sqrt(ps[idx-1].F0Hz() * ps[idx+1].F0Hz())
	if c := math.Abs(math.Log2(got/want)) * 1200; c > 100 {
		t.Fatalf("concealed f0 %.1f Hz, neighbours imply %.1f Hz (%.0f cents)", got, want, c)
	}
	if tc.Stats.Interpolated+tc.Stats.PartialConceal == 0 {
		t.Fatal("concealment path not exercised")
	}
}

func BenchmarkDStarToDMR(b *testing.B) {
	t := &testing.T{}
	vec := vectors(t)
	tc := New(ambe.DStar, ambe.DMR, DefaultOptions)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tc.Frame(vec[i%len(vec)])
	}
}
