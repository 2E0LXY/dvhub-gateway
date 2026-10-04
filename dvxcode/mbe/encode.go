package mbe

import (
	"math"
	"sort"

	"github.com/2E0LXY/dvxcode/ambe"
)

// Encoder quantises MBE parameters to AMBE / AMBE+2 bits.
//
// It is closed-loop: a shadow Decoder tracks the receiver's prediction
// state exactly, so the encoder never drifts from what radios reconstruct.
// Anti-warble measures: pitch-index and V/UV-codebook hysteresis, exact
// weighted search of the spectral codebooks.
type Encoder struct {
	mode    ambe.Mode
	cb      *codebooks
	shadow  *Decoder
	prevB0  int
	prevB1  int
	hasPrev bool
	silence ambe.Bits49
	Opt     EncoderOptions
	Stats   EncoderStats
}

// EncoderOptions tunes anti-warble behaviour.
type EncoderOptions struct {
	PitchHysteresis float64 // keep previous b0 if within this many index steps of optimal (0 disables)
	VUVHysteresis   float64 // keep previous b1 if its mismatch exceeds the best by < this fraction of total weight
	TopK            int     // PRBA candidates per codebook for the joint search
}

// DefaultEncoderOptions are tuned for minimal frame-to-frame churn.
var DefaultEncoderOptions = EncoderOptions{PitchHysteresis: 0.6, VUVHysteresis: 0.03, TopK: 16}

// EncoderStats accumulates quantisation diagnostics.
type EncoderStats struct {
	Frames      int
	PitchHolds  int     // frames where hysteresis kept the previous pitch index
	VUVHolds    int     // frames where hysteresis kept the previous V/UV index
	SqErrLog2   float64 // sum of squared log2 magnitude error (decoded vs target)
	ErrHarmonic int
}

// NewEncoder returns an encoder whose shadow decoder starts in the same
// state as a freshly initialised receiver.
func NewEncoder(m ambe.Mode) *Encoder {
	e := &Encoder{mode: m, cb: &cbDMR, shadow: NewDecoder(m), Opt: DefaultEncoderOptions}
	nullDStar := [9]byte{0x9E, 0x8D, 0x32, 0x88, 0x26, 0x1A, 0x3F, 0x61, 0xE8}
	nullDMR := [9]byte{0xB9, 0xE8, 0x81, 0x52, 0x61, 0x73, 0x00, 0x2A, 0x6B}
	if m == ambe.DStar {
		e.cb = &cbDStar
		e.silence, _ = ambe.Decode(m, nullDStar)
	} else {
		e.silence, _ = ambe.Decode(m, nullDMR)
	}
	return e
}

// Reset restores the start-of-transmission state.
func (e *Encoder) Reset() {
	e.shadow = NewDecoder(e.mode)
	e.hasPrev = false
}

// Shadow returns the receiver-side parameters for the last encoded frame.
func (e *Encoder) Shadow() Params { return e.shadow.Prev() }

// F0Index returns the normalised fundamental (cycles/sample) for a b0 index.
func F0Index(m ambe.Mode, b0 int) float64 {
	if m == ambe.DMR {
		return float64(AmbeW0table[b0])
	}
	return float64(float32(math.Pow(2, float64(float32(-4.311767578125-2.1336e-2*(float64(b0)+0.5))))))
}

// MaxVoiceB0 is the highest pitch index used for voice. DMR reserves
// 120..127 (erasure/silence/tone). Real D-STAR streams use 120..125 for voice
// (observed in captured speech); only 126/127 signal tones. D-STAR silence is
// the specific null frame, not a b0 value.
func MaxVoiceB0(m ambe.Mode) int {
	if m == ambe.DStar {
		return 125
	}
	return 119
}

// IsNull reports whether bits are the mode's silence/null frame.
func (e *Encoder) IsNull(b ambe.Bits49) bool { return b == e.silence }

// EncodeSilence emits the mode's silence frame.
func (e *Encoder) EncodeSilence() ambe.Bits49 {
	e.shadow.Decode(e.silence)
	e.hasPrev = false
	return e.silence
}

// envelope samples a Params' log2 magnitude and voicing at frequency w (rad/sample).
type envelope struct {
	p *Params
}

func (v envelope) at(w float64) (log2m float64, voiced float64) {
	p := v.p
	x := w / float64(p.W0) // fractional harmonic index
	if x <= 1 {
		return float64(p.Log2Ml[1]), float64(p.Vl[1])
	}
	if x >= float64(p.L) {
		return float64(p.Log2Ml[p.L]), float64(p.Vl[p.L])
	}
	i := int(x)
	f := x - float64(i)
	m := float64(p.Log2Ml[i])*(1-f) + float64(p.Log2Ml[i+1])*f
	vo := float64(p.Vl[i])*(1-f) + float64(p.Vl[i+1])*f
	return m, vo
}

// Encode quantises one voiced/unvoiced frame described by target (any w0
// grid, e.g. the source codec's decoded parameters) and returns the bits.
func (e *Encoder) Encode(target Params) ambe.Bits49 {
	var ix Indices
	env := envelope{&target}

	// --- Pitch (b0) with hysteresis.
	fT := float64(target.W0) / (2 * math.Pi)
	best, bestD := 0, math.Inf(1)
	for b := 0; b <= MaxVoiceB0(e.mode); b++ {
		if d := math.Abs(math.Log2(F0Index(e.mode, b) / fT)); d < bestD {
			best, bestD = b, d
		}
	}
	step := 0.0215 // octaves per index (both tables)
	if e.hasPrev && e.Opt.PitchHysteresis > 0 && e.prevB0 != best {
		dPrev := math.Abs(math.Log2(F0Index(e.mode, e.prevB0) / fT))
		if dPrev-bestD < e.Opt.PitchHysteresis*step {
			best = e.prevB0
			e.Stats.PitchHolds++
		}
	}
	ix.B0 = best
	f0 := F0Index(e.mode, ix.B0)
	w0 := 2 * math.Pi * f0
	L := int(e.cb.ltable[ix.B0])

	var T [MaxL + 2]float64
	var V [MaxL + 2]float64
	var wt [MaxL + 2]float64
	var wsum float64
	for l := 1; l <= L; l++ {
		T[l], V[l] = env.at(w0 * float64(l))
		wt[l] = math.Exp2(T[l])
		wsum += wt[l]
	}

	// --- V/UV (b1): amplitude-weighted mismatch, with hysteresis.
	nVuv := 32
	if e.mode == ambe.DStar {
		nVuv = 16
	}
	vuv := func(b1, l int) float64 {
		jl := int(float32(l) * 16 * float32(f0))
		if e.mode == ambe.DStar {
			return float64(AmbePlusVuv[b1][jl])
		}
		return float64(AmbeVuv[b1][jl])
	}
	costs := make([]float64, nVuv)
	bb := 0
	for b1 := 0; b1 < nVuv; b1++ {
		var c float64
		for l := 1; l <= L; l++ {
			c += wt[l] * math.Abs(vuv(b1, l)-V[l])
		}
		costs[b1] = c
		if c < costs[bb] {
			bb = b1
		}
	}
	if e.hasPrev && bb != e.prevB1 && costs[e.prevB1]-costs[bb] < e.Opt.VUVHysteresis*wsum {
		bb = e.prevB1
		e.Stats.VUVHolds++
	}
	ix.B1 = bb

	// --- Gain (b2): decoded mean(log2Ml) == gamma - 0.5*log2(L).
	prev := e.shadow.Prev()
	var meanT float64
	for l := 1; l <= L; l++ {
		meanT += T[l]
	}
	meanT /= float64(L)
	gT := meanT + 0.5*math.Log2(float64(L))
	bg, bgd := 0, math.Inf(1)
	for b2, dg := range e.cb.dg {
		if d := math.Abs(float64(dg) + 0.5*float64(prev.Gamma) - gT); d < bgd {
			bg, bgd = b2, d
		}
	}
	ix.B2 = bg

	// --- Prediction residual, exactly as the decoder forms its prediction.
	pl := prev.Log2Ml
	if L > prev.L {
		for l := prev.L + 1; l <= L; l++ {
			pl[l] = pl[prev.L]
		}
	}
	pl[0] = pl[1]
	var R [MaxL + 2]float64
	for l := 1; l <= L; l++ {
		fl := (float32(prev.L) / float32(L)) * float32(l)
		k := int(fl)
		dl := float64(fl - float32(k))
		R[l] = T[l] - 0.65*((1-dl)*float64(pl[k])+dl*float64(pl[k+1]))
	}

	// --- Block DCT (inverse of the decoder's IDCT).
	var Ji [5]int
	for i := 0; i < 4; i++ {
		Ji[i+1] = int(e.cb.lmprbl[L][i])
	}
	var C [5][18]float64
	l0 := 1
	for i := 1; i <= 4; i++ {
		J := Ji[i]
		for k := 1; k <= J; k++ {
			var s float64
			for j := 1; j <= J; j++ {
				s += R[l0+j-1] * math.Cos(math.Pi*float64(k-1)*(float64(j)-0.5)/float64(J))
			}
			C[i][k] = s / float64(J)
		}
		l0 += J
	}

	// --- PRBA (b3, b4): exact quadratic-form search.
	ix.B3, ix.B4 = e.searchPRBA(&C, &Ji, L)

	// --- HOC (b5..b8): per-block nearest neighbour (orthogonal to PRBA).
	hoc := [5][][4]float32{nil, e.cb.hoc5, e.cb.hoc6, e.cb.hoc7, e.cb.hoc8}
	var hix [5]int
	for i := 1; i <= 4; i++ {
		dims := min(Ji[i], 6) - 2
		bi, bd := 0, math.Inf(1)
		for n, v := range hoc[i] {
			if e.mode == ambe.DStar && i == 4 && n&1 == 1 {
				continue // LSB of D-STAR b8 is not transmitted
			}
			var d float64
			for k := 0; k < dims; k++ {
				x := float64(v[k]) - C[i][k+3]
				d += x * x
			}
			if d < bd {
				bi, bd = n, d
			}
		}
		hix[i] = bi
	}
	ix.B5, ix.B6, ix.B7, ix.B8 = hix[1], hix[2], hix[3], hix[4]

	bits := Pack(e.mode, ix)
	got, _ := e.shadow.Decode(bits)
	e.Stats.Frames++
	for l := 1; l <= L; l++ {
		d := float64(got.Log2Ml[l]) - T[l]
		e.Stats.SqErrLog2 += d * d
		e.Stats.ErrHarmonic++
	}
	e.prevB0, e.prevB1, e.hasPrev = ix.B0, ix.B1, true
	return bits
}

func (e *Encoder) searchPRBA(C *[5][18]float64, Ji *[5]int, L int) (int, int) {
	rc := 1 / (2 * math.Sqrt2)
	// Target G (m = 2..8) from C[i][1..2].
	var R8 [9]float64
	for i := 1; i <= 4; i++ {
		R8[2*i-1] = C[i][1] + C[i][2]/(2*rc)
		R8[2*i] = C[i][1] - C[i][2]/(2*rc)
	}
	var G [9]float64
	for m := 2; m <= 8; m++ {
		for i := 1; i <= 8; i++ {
			G[m] += R8[i] * math.Cos(math.Pi*float64(m-1)*(float64(i)-0.5)/8)
		}
		G[m] /= 8
	}
	// Linear map ΔG(7) -> v = [ΔC1_1..4, ΔC2_1..4]; error = vᵀDv - (jᵀv)²/L.
	var A [9][9]float64 // A[i][m] : ΔR8_i per ΔG_m
	for i := 1; i <= 8; i++ {
		for m := 2; m <= 8; m++ {
			A[i][m] = 2 * math.Cos(math.Pi*float64(m-1)*(float64(i)-0.5)/8)
		}
	}
	var M [8][9]float64
	for i := 1; i <= 4; i++ {
		for m := 2; m <= 8; m++ {
			M[i-1][m] = 0.5 * (A[2*i-1][m] + A[2*i][m])
			M[i+3][m] = rc * (A[2*i-1][m] - A[2*i][m])
		}
	}
	var Dw, jv [8]float64
	for i := 1; i <= 4; i++ {
		Dw[i-1] = float64(Ji[i])
		jv[i-1] = float64(Ji[i])
		if Ji[i] >= 2 {
			Dw[i+3] = 2 * float64(Ji[i])
		}
	}
	var Q [9][9]float64
	for a := 2; a <= 8; a++ {
		for b := 2; b <= 8; b++ {
			var s, ja, jb float64
			for r := 0; r < 8; r++ {
				s += M[r][a] * Dw[r] * M[r][b]
				ja += jv[r] * M[r][a]
				jb += jv[r] * M[r][b]
			}
			Q[a][b] = s - ja*jb/float64(L)
		}
	}
	quad := func(d *[9]float64, lo, hi int) float64 {
		var s float64
		for a := lo; a <= hi; a++ {
			for b := lo; b <= hi; b++ {
				s += d[a] * Q[a][b] * d[b]
			}
		}
		return s
	}
	type cand struct {
		i int
		e float64
		d [9]float64
	}
	var c3 []cand
	for n, v := range e.cb.prba24 {
		var d [9]float64
		for k := 0; k < 3; k++ {
			d[2+k] = float64(v[k]) - G[2+k]
		}
		c3 = append(c3, cand{n, quad(&d, 2, 4), d})
	}
	var c4 []cand
	for n, v := range e.cb.prba58 {
		var d [9]float64
		for k := 0; k < 4; k++ {
			d[5+k] = float64(v[k]) - G[5+k]
		}
		c4 = append(c4, cand{n, quad(&d, 5, 8), d})
	}
	K := max(1, e.Opt.TopK)
	sort.Slice(c3, func(a, b int) bool { return c3[a].e < c3[b].e })
	sort.Slice(c4, func(a, b int) bool { return c4[a].e < c4[b].e })
	b3, b4, be := 0, 0, math.Inf(1)
	for _, x := range c3[:min(K, len(c3))] {
		for _, y := range c4[:min(K, len(c4))] {
			d := x.d
			for m := 5; m <= 8; m++ {
				d[m] = y.d[m]
			}
			if v := quad(&d, 2, 8); v < be {
				b3, b4, be = x.i, y.i, v
			}
		}
	}
	return b3, b4
}
