package mbe

import "math"

// Resample re-expresses p on the harmonic grid of fundamental w0 (rad/sample),
// keeping the spectral envelope and voicing as functions of frequency.
func Resample(p Params, w0 float32) Params {
	q := Params{W0: w0, Gamma: p.Gamma}
	q.L = max(9, min(MaxL, int(0.4627*2*math.Pi/float64(w0))))
	env := envelope{&p}
	for l := 1; l <= q.L; l++ {
		m, v := env.at(float64(w0) * float64(l))
		q.Log2Ml[l] = float32(m)
		if v >= 0.5 {
			q.Vl[l] = 1
		}
	}
	return q
}

// VoicedFraction returns the fraction of harmonics below maxHz that are voiced.
func (p *Params) VoicedFraction(maxHz float64) float64 {
	n, v := 0, 0
	for l := 1; l <= p.L; l++ {
		if float64(l)*float64(p.W0)/(2*math.Pi)*8000 > maxHz {
			break
		}
		n++
		v += int(p.Vl[l])
	}
	if n == 0 {
		return 0
	}
	return float64(v) / float64(n)
}

// EnvelopeAt returns log2 magnitude and voicing (0..1) at frequency hz.
func (p *Params) EnvelopeAt(hz float64) (float64, float64) {
	return envelope{p}.at(2 * math.Pi * hz / 8000)
}

// Interpolate returns the midpoint of a and b: geometric-mean pitch,
// averaged log envelope, majority voicing (ties voiced).
func Interpolate(a, b Params) Params {
	w := float32(math.Sqrt(float64(a.W0) * float64(b.W0)))
	ra, rb := Resample(a, w), Resample(b, w)
	q := ra
	q.Gamma = (a.Gamma + b.Gamma) / 2
	for l := 1; l <= q.L; l++ {
		q.Log2Ml[l] = (ra.Log2Ml[l] + rb.Log2Ml[l]) / 2
		q.Vl[l] = 0
		if ra.Vl[l]+rb.Vl[l] >= 1 {
			q.Vl[l] = 1
		}
	}
	return q
}

// MeanLog2 returns the mean log2 magnitude over harmonics 1..L.
func (p *Params) MeanLog2() float32 {
	var s float32
	for l := 1; l <= p.L; l++ {
		s += p.Log2Ml[l]
	}
	return s / float32(max(1, p.L))
}
