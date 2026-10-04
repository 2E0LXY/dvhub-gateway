package mbe

import (
	"math"
	"math/rand"

	"github.com/2E0LXY/dvxcode/internal/dsp"
)

// FrameLen is samples per 20 ms frame at 8 kHz.
const FrameLen = 160

// Synth renders MBE parameters to PCM (int16 sample scale, returned as
// float64 in [-1,1)). Used for listening, the tandem baseline and test
// material; it is not a model of any DVSI decoder.
//
// Conventions (shared with Analyser so the pair round-trips):
//   - voiced harmonic l: sinusoid of amplitude 2^Log2Ml[l]
//   - unvoiced harmonic band l: noise of the same band power (A²/2)
type Synth struct {
	prev  Params
	phase [MaxL + 2]float64
	rng   *rand.Rand
	ola   [FrameLen]float64 // unvoiced overlap tail
	win   [2 * FrameLen]float64
	buf   []complex128
}

const synthFFT = 512

// NewSynth returns a synthesiser starting from silence.
func NewSynth(seed int64) *Synth {
	s := &Synth{rng: rand.New(rand.NewSource(seed)), buf: make([]complex128, synthFFT)}
	s.prev.W0, s.prev.L = 2*math.Pi/32, 14
	for i := range s.win { // sine window: w[n]^2 + w[n+N]^2 == 1
		s.win[i] = math.Sin(math.Pi * (float64(i) + 0.5) / (2 * FrameLen))
	}
	for l := range s.prev.Log2Ml {
		s.prev.Log2Ml[l] = -30
	}
	return s
}

func amp(p *Params, l int) float64 {
	if l < 1 || l > p.L {
		return 0
	}
	return math.Exp2(float64(p.Log2Ml[l]))
}

// Frame synthesises 160 samples transitioning from the previous frame to p.
// Pass mute=true to fade to silence (p is then ignored for content).
func (s *Synth) Frame(p Params, mute bool) []float64 {
	out := make([]float64, FrameLen)
	if mute {
		p = Params{W0: s.prev.W0, L: s.prev.L}
		for l := range p.Log2Ml {
			p.Log2Ml[l] = -30
		}
	}
	cw, pw := float64(p.W0), float64(s.prev.W0)
	maxL := max(p.L, s.prev.L)
	const N = FrameLen

	// Voiced part.
	for l := 1; l <= maxL; l++ {
		cv := l <= p.L && p.Vl[l] == 1
		pv := l <= s.prev.L && s.prev.Vl[l] == 1
		if !cv && !pv {
			s.phase[l] += (pw + cw) * float64(l) * N / 2
			continue
		}
		ap, ac := 0.0, 0.0
		if pv {
			ap = amp(&s.prev, l)
		}
		if cv {
			ac = amp(&p, l)
		}
		fl := float64(l)
		if cv && pv && math.Abs(cw-pw) < 0.1*cw && fl*math.Max(cw, pw) < math.Pi {
			// Continuous phase, linear frequency and amplitude interpolation.
			ph := s.phase[l]
			for n := 0; n < N; n++ {
				a := float64(n) / N
				w := fl * (pw + (cw-pw)*a)
				out[n] += (ap + (ac-ap)*a) * math.Cos(ph)
				ph += w
			}
			s.phase[l] = math.Mod(ph, 2*math.Pi)
			continue
		}
		// Cross-fade: previous component decays, current rises.
		ph0 := s.phase[l]
		for n := 0; n < N; n++ {
			fo := s.win[n+N] * s.win[n+N] // 1 -> 0
			fi := 1 - fo
			if pv && fl*pw < math.Pi {
				out[n] += fo * ap * math.Cos(ph0+fl*pw*float64(n))
			}
			if cv && fl*cw < math.Pi {
				out[n] += fi * ac * math.Cos(ph0+fl*cw*float64(n))
			}
		}
		s.phase[l] = math.Mod(ph0+fl*cw*N, 2*math.Pi)
	}

	// Unvoiced part: shaped noise, sine-windowed 50% overlap-add (sin²+cos²=1,
	// so the noise power is constant across the overlap).
	uv := false
	for l := 1; l <= p.L; l++ {
		if p.Vl[l] == 0 {
			uv = true
			break
		}
	}
	seg := make([]float64, 2*N)
	if uv {
		for i := range s.buf {
			s.buf[i] = 0
		}
		for i := 0; i < 2*N; i++ {
			s.buf[i] = complex(s.rng.NormFloat64(), 0)
		}
		dsp.FFT(s.buf, false)
		// Per-band gain so band power equals A²/2 (see derivation in README).
		bin := 2 * math.Pi / synthFFT
		count := make([]int, p.L+2)
		band := make([]int, synthFFT/2+1)
		for k := 1; k <= synthFFT/2; k++ {
			l := int(math.Round(float64(k) * bin / cw))
			if l >= 1 && l <= p.L && p.Vl[l] == 0 {
				band[k] = l
				count[l]++
			}
		}
		for k := 0; k <= synthFFT/2; k++ {
			g := 0.0
			if l := band[k]; l > 0 && k > 0 {
				// Unit noise over 2N samples: E|X_k|^2 = 2N. After IFFT (/M) the
				// band power is 2N*2*count*g^2/M^2; set equal to A^2/2.
				g = amp(&p, l) * synthFFT / math.Sqrt(8*N*float64(count[l]))
			}
			s.buf[k] *= complex(g, 0)
			if k > 0 && k < synthFFT/2 {
				s.buf[synthFFT-k] *= complex(g, 0)
			}
		}
		dsp.FFT(s.buf, true)
		for i := 0; i < 2*N; i++ {
			seg[i] = real(s.buf[i]) / synthFFT * s.win[i]
		}
	}
	for n := 0; n < N; n++ {
		out[n] += s.ola[n] + seg[n]
		s.ola[n] = seg[n+N]
		out[n] /= 32768
	}
	s.prev = p
	return out
}
