package mbe

import (
	"math"

	"github.com/2E0LXY/dvxcode/internal/dsp"
)

// Analysis (PCM -> MBE parameters). Offline, whole-signal pitch tracking.
// Used for test material and the tandem baseline only: the parametric
// transcoder never analyses audio.

const (
	anaWin  = 301 // samples (37.6 ms)
	anaHalf = anaWin / 2
	anaFFT  = 512
	wOver   = 16 // window-spectrum table oversampling
	wSpan   = 32 // bins
	fMin    = 55.0
	fMax    = 400.0
	fSteps  = 36 // candidates per octave
	pitchHz = 1200.0
)

// Frame is one analysed frame.
type Frame struct {
	P    Params
	Kind Kind // Voice or Silence
	F0   float64
	Err  float64 // normalised harmonic-fit error at F0 (0 = perfectly periodic)
}

type analyser struct {
	win  []float64
	wtab []float64 // W(d), d = i/wOver - wSpan
	sw2  float64
}

func newAnalyser() *analyser {
	a := &analyser{win: dsp.Hamming(anaWin)}
	for _, w := range a.win {
		a.sw2 += w * w
	}
	a.wtab = make([]float64, 2*wSpan*wOver+1)
	for i := range a.wtab {
		d := float64(i)/wOver - wSpan
		var s float64
		for t := -anaHalf; t <= anaHalf; t++ {
			s += a.win[t+anaHalf] * math.Cos(2*math.Pi*d*float64(t)/anaFFT)
		}
		a.wtab[i] = s
	}
	return a
}

func (a *analyser) W(d float64) float64 {
	i := int(math.Round((d + wSpan) * wOver))
	if i < 0 || i >= len(a.wtab) {
		return 0
	}
	return a.wtab[i]
}

// spectrum of the window centred on sample c (window time origin at bin phase 0).
func (a *analyser) spectrum(x []float64, c int) []complex128 {
	buf := make([]complex128, anaFFT)
	for t := -anaHalf; t <= anaHalf; t++ {
		i := c + t
		v := 0.0
		if i >= 0 && i < len(x) {
			v = x[i] * 32768
		}
		buf[(t+anaFFT)%anaFFT] = complex(v*a.win[t+anaHalf], 0)
	}
	dsp.FFT(buf, false)
	return buf[:anaFFT/2+1]
}

// fit returns per-harmonic band energy, residual after a single-sinusoid
// least-squares fit, for harmonics up to maxHz.
func (a *analyser) fit(X []complex128, f0, maxHz float64, e, r []float64) int {
	b := f0 * anaFFT / 8000
	L := int(maxHz / f0)
	for l := 1; l <= L; l++ {
		lo := int(math.Ceil((float64(l) - 0.5) * b))
		hi := int(math.Floor((float64(l) + 0.5) * b))
		if hi > anaFFT/2 {
			hi = anaFFT / 2
		}
		var num complex128
		var den, en float64
		for k := lo; k <= hi; k++ {
			w := a.W(float64(k) - float64(l)*b)
			num += X[k] * complex(w, 0)
			den += w * w
			en += real(X[k])*real(X[k]) + imag(X[k])*imag(X[k])
		}
		res := en
		if den > 0 {
			res = en - (real(num)*real(num)+imag(num)*imag(num))/den
		}
		e[l], r[l] = en, math.Max(res, 0)
	}
	return L
}

func (a *analyser) pitchError(X []complex128, f0 float64) float64 {
	var e, r [128]float64
	L := a.fit(X, f0, pitchHz, e[:], r[:])
	var se, sr float64
	for l := 1; l <= L; l++ {
		se += e[l]
		sr += r[l]
	}
	if se == 0 {
		return 1
	}
	return sr / se
}

// Analyse converts 8 kHz PCM (float, ±1 full scale) to MBE frames.
// Frame n describes the signal at sample (n+1)*160, matching Synth timing.
func Analyse(x []float64) []Frame {
	a := newAnalyser()
	nf := len(x) / FrameLen
	nc := int(math.Log2(fMax/fMin)*fSteps) + 1
	cand := make([]float64, nc)
	for i := range cand {
		cand[i] = fMin * math.Pow(2, float64(i)/fSteps)
	}
	specs := make([][]complex128, nf)
	errs := make([][]float64, nf)
	energy := make([]float64, nf)
	for n := 0; n < nf; n++ {
		c := (n + 1) * FrameLen
		X := a.spectrum(x, c)
		specs[n] = X
		var en float64
		for _, v := range X {
			en += real(v)*real(v) + imag(v)*imag(v)
		}
		energy[n] = en
		raw := make([]float64, nc)
		hi := make([]float64, 0, 2) // errors at 2f and 3f (beyond grid: computed directly)
		for i, f := range cand {
			raw[i] = a.pitchError(X, f)
		}
		errs[n] = make([]float64, nc)
		for i, f := range cand {
			// Sub-multiple guard: for f = f0/k every k-th band is empty, so the
			// fit at f is about as good as at k*f. Penalise f when a multiple
			// fits nearly as well; at the true f0 the multiples fit badly.
			hi = hi[:0]
			for _, k := range []float64{2, 3} {
				j := i + int(math.Round(math.Log2(k)*fSteps))
				if j < nc {
					hi = append(hi, raw[j])
				} else if f*k <= 1000 {
					hi = append(hi, a.pitchError(X, f*k))
				}
			}
			pen := 0.0
			for _, h := range hi {
				pen = math.Max(pen, 0.6*math.Max(0, math.Min(1, (raw[i]+0.15-h)/0.15)))
			}
			errs[n][i] = raw[i] + pen + 0.02*math.Log2(fMax/f)
		}
	}

	// Viterbi pitch track: transition cost ∝ |Δ log2 f0|, scaled by how
	// periodic both frames are (unvoiced frames track loosely).
	const lambda = 0.35
	conf := make([]float64, nf)
	for n := range errs {
		m := math.Inf(1)
		for _, v := range errs[n] {
			m = math.Min(m, v)
		}
		conf[n] = math.Max(0, math.Min(1, 1-2*m))
	}
	cost := make([]float64, nc)
	back := make([][]int32, nf)
	for n := 0; n < nf; n++ {
		back[n] = make([]int32, nc)
		nxt := make([]float64, nc)
		var minPrev float64 = math.Inf(1)
		for _, c := range cost {
			minPrev = math.Min(minPrev, c)
		}
		for i := range cand {
			best, bj := math.Inf(1), 0
			if n == 0 {
				best = 0
			} else {
				for j := range cand {
					d := math.Abs(float64(i-j)) / fSteps
					v := cost[j] + lambda*d*conf[n]*conf[n-1]
					if v < best {
						best, bj = v, j
					}
				}
			}
			nxt[i] = best + errs[n][i]
			back[n][i] = int32(bj)
		}
		cost = nxt
	}
	path := make([]int, nf)
	if nf > 0 {
		bi := 0
		for i := range cost {
			if cost[i] < cost[bi] {
				bi = i
			}
		}
		for n := nf - 1; n >= 0; n-- {
			path[n] = bi
			bi = int(back[n][bi])
		}
	}

	// Silence threshold relative to the loudest frame (and absolute floor).
	var peak float64
	for _, e := range energy {
		peak = math.Max(peak, e)
	}
	out := make([]Frame, nf)
	var e, r [MaxL + 2]float64
	for n := 0; n < nf; n++ {
		f0 := cand[path[n]]
		// Local refinement ±1/2 grid step.
		bestE := a.pitchError(specs[n], f0)
		for _, m := range []float64{-0.5, -0.25, 0.25, 0.5} {
			f := f0 * math.Pow(2, m/fSteps)
			if v := a.pitchError(specs[n], f); v < bestE {
				bestE, f0 = v, f
			}
		}
		fr := &out[n]
		fr.F0, fr.Err = f0, bestE
		p := &fr.P
		p.W0 = float32(2 * math.Pi * f0 / 8000)
		p.L = max(9, min(MaxL, int(0.4627*8000/f0)))
		if energy[n] < peak*1e-5 || energy[n] < 1e6 {
			fr.Kind = Silence
			for l := 1; l <= p.L; l++ {
				p.Log2Ml[l] = 0
			}
			continue
		}
		fr.Kind = Voice
		a.fit(specs[n], f0, f0*float64(p.L)+1, e[:], r[:])
		theta := 0.25
		if bestE < 0.15 {
			theta = 0.35 // strongly periodic frame: accept weaker bands
		}
		for l := 1; l <= p.L; l++ {
			A := math.Sqrt(4 * e[l] / (anaFFT * a.sw2))
			p.Log2Ml[l] = float32(math.Log2(math.Max(A, 1e-3)))
			if e[l] > 0 && r[l]/e[l] < theta && bestE < 0.6 {
				p.Vl[l] = 1
			}
		}
	}
	return out
}
