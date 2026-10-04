package main

import (
	"math"
	"sort"

	"github.com/2E0LXY/dvxcode/mbe"
)

// frameP is a decoded frame for evaluation; Silent covers silence/tone/mute.
type frameP struct {
	P      mbe.Params
	Silent bool
}

func (f frameP) f0() float64 { return f.P.F0Hz() }

func (f frameP) isVoiced() bool { return !f.Silent && f.P.VoicedFraction(1000) >= 0.5 }

// Metrics compare a candidate stream against a reference stream.
type Metrics struct {
	Frames        int
	VoicedPairs   int
	MedianCents   float64 // |f0 error|, voiced in both
	PitchErrPct   float64 // > 50 cents
	OctavePct     float64 // > 600 cents
	VUVAgreePct   float64
	LSDdB         float64 // RMS log-spectral distance, 100..3600 Hz
	JitterRef     float64 // mean |Δf0| between consecutive voiced frames (cents)
	JitterOut     float64
	FluxRef       float64 // mean frame-to-frame spectral change (dB)
	FluxOut       float64
	SilenceAgrees float64
}

func cents(a, b float64) float64 { return 1200 * math.Abs(math.Log2(a/b)) }

func lsd(a, b *mbe.Params) float64 {
	var s float64
	n := 0
	for hz := 100.0; hz <= 3600; hz += 50 {
		x, _ := a.EnvelopeAt(hz)
		y, _ := b.EnvelopeAt(hz)
		d := 6.0206 * (x - y)
		s += d * d
		n++
	}
	return math.Sqrt(s / float64(n))
}

func compare(ref, out []frameP) Metrics {
	n := min(len(ref), len(out))
	var m Metrics
	m.Frames = n
	var cs []float64
	var pe, oe, vAgree, vTot, silAgree int
	var lsdSum float64
	var lsdN int
	var jr, jo, fr, fo float64
	var jn, fn int
	for i := 0; i < n; i++ {
		r, o := ref[i], out[i]
		if r.Silent == o.Silent {
			silAgree++
		}
		if r.Silent || o.Silent {
			continue
		}
		d := lsd(&r.P, &o.P)
		lsdSum += d * d
		lsdN++
		for hz := 100.0; hz <= 3600; hz += 50 {
			_, a := r.P.EnvelopeAt(hz)
			_, b := o.P.EnvelopeAt(hz)
			vTot++
			if (a >= 0.5) == (b >= 0.5) {
				vAgree++
			}
		}
		if r.isVoiced() && o.isVoiced() {
			c := cents(r.f0(), o.f0())
			cs = append(cs, c)
			if c > 50 {
				pe++
			}
			if c > 600 {
				oe++
			}
		}
		if i > 0 && !ref[i-1].Silent && !out[i-1].Silent {
			if r.isVoiced() && ref[i-1].isVoiced() && o.isVoiced() && out[i-1].isVoiced() {
				jr += cents(r.f0(), ref[i-1].f0())
				jo += cents(o.f0(), out[i-1].f0())
				jn++
			}
			fr += lsd(&r.P, &ref[i-1].P)
			fo += lsd(&o.P, &out[i-1].P)
			fn++
		}
	}
	m.VoicedPairs = len(cs)
	if len(cs) > 0 {
		sort.Float64s(cs)
		m.MedianCents = cs[len(cs)/2]
		m.PitchErrPct = 100 * float64(pe) / float64(len(cs))
		m.OctavePct = 100 * float64(oe) / float64(len(cs))
	}
	if vTot > 0 {
		m.VUVAgreePct = 100 * float64(vAgree) / float64(vTot)
	}
	if lsdN > 0 {
		m.LSDdB = math.Sqrt(lsdSum / float64(lsdN))
	}
	if jn > 0 {
		m.JitterRef, m.JitterOut = jr/float64(jn), jo/float64(jn)
	}
	if fn > 0 {
		m.FluxRef, m.FluxOut = fr/float64(fn), fo/float64(fn)
	}
	if n > 0 {
		m.SilenceAgrees = 100 * float64(silAgree) / float64(n)
	}
	return m
}
