package mbe

import (
	"math"
	"testing"
)

// vowel synthesises a pulse train through two formant resonators.
func vowel(f0 func(t float64) float64, secs float64) []float64 {
	n := int(secs * 8000)
	x := make([]float64, n)
	ph := 0.0
	type res struct{ a1, a2, y1, y2 float64 }
	mk := func(fc, bw float64) *res {
		r := math.Exp(-math.Pi * bw / 8000)
		return &res{a1: 2 * r * math.Cos(2*math.Pi*fc/8000), a2: -r * r}
	}
	f1, f2 := mk(700, 90), mk(1200, 110)
	for i := range x {
		ph += f0(float64(i)/8000) / 8000
		e := 0.0
		if ph >= 1 {
			ph -= 1
			e = 1
		}
		y := e + f1.a1*f1.y1 + f1.a2*f1.y2
		f1.y2, f1.y1 = f1.y1, y
		z := y + f2.a1*f2.y1 + f2.a2*f2.y2
		f2.y2, f2.y1 = f2.y1, z
		x[i] = z
	}
	var pk float64
	for _, v := range x {
		pk = math.Max(pk, math.Abs(v))
	}
	for i := range x {
		x[i] *= 0.5 / pk
	}
	return x
}

func TestAnalysePitchGlide(t *testing.T) {
	f := func(t float64) float64 { return 90 + 120*t } // 90 -> 210 Hz over 1 s
	x := vowel(f, 1.0)
	fr := Analyse(x)
	var worst float64
	for n := 3; n < len(fr)-3; n++ {
		truth := f(float64(n+1) * FrameLen / 8000)
		c := 1200 * math.Abs(math.Log2(fr[n].F0/truth))
		worst = math.Max(worst, c)
		if fr[n].Kind != Voice {
			t.Fatalf("frame %d not voice", n)
		}
	}
	if worst > 50 {
		t.Fatalf("worst pitch error %.0f cents", worst)
	}
	t.Logf("glide 90-210 Hz: worst error %.1f cents over %d frames", worst, len(fr)-6)
}

func TestSynthAnalyseRoundTrip(t *testing.T) {
	x := vowel(func(float64) float64 { return 120 }, 0.6)
	fr := Analyse(x)
	s := NewSynth(1)
	var y []float64
	for _, f := range fr {
		y = append(y, s.Frame(f.P, f.Kind != Voice)...)
	}
	fr2 := Analyse(y)
	var d, n float64
	for i := 5; i < len(fr2)-5; i++ {
		a, b := fr[i].P, fr2[i].P
		if math.Abs(fr[i].F0-fr2[i].F0) > 3 {
			t.Fatalf("frame %d f0 %.1f -> %.1f", i, fr[i].F0, fr2[i].F0)
		}
		for l := 1; l <= min(a.L, 20); l++ {
			v := float64(a.Log2Ml[l] - b.Log2Ml[l])
			d += v * v
			n++
		}
	}
	rms := 6.02 * math.Sqrt(d/n)
	t.Logf("analyse->synth->analyse: rms magnitude error %.2f dB (harmonics 1..20)", rms)
	if rms > 3 {
		t.Fatalf("round trip magnitude error %.2f dB", rms)
	}
}
