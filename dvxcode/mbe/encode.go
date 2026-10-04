package mbe

import (
	"math"

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
	Rejected    int     // non-finite or out-of-range targets replaced by silence
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

// sane rejects targets that would poison the quantiser (NaN/Inf, absurd
// pitch or level). Log magnitudes are clamped to a safe range in place.
func sane(p *Params) bool {
	w := float64(p.W0)
	if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 || w > math.Pi/4 || p.L < 1 || p.L > MaxL {
		return false
	}
	for l := 1; l <= p.L; l++ {
		v := float64(p.Log2Ml[l])
		if math.IsNaN(v) {
			return false
		}
		p.Log2Ml[l] = float32(max(-20, min(v, 20)))
	}
	return true
}

var log2F0 [2][128]float64

func init() {
	for _, m := range []ambe.Mode{ambe.DStar, ambe.DMR} {
		for b := 0; b <= MaxVoiceB0(m); b++ {
			log2F0[m][b] = math.Log2(F0Index(m, b))
		}
	}
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
	if !sane(&target) {
		e.Stats.Rejected++
		return e.EncodeSilence()
	}
	var ix Indices
	env := envelope{&target}

	// --- Pitch (b0) with hysteresis.
	lT := math.Log2(float64(target.W0) / (2 * math.Pi))
	lt := &log2F0[e.mode]
	best, bestD := 0, math.Inf(1)
	for b := 0; b <= MaxVoiceB0(e.mode); b++ {
		if d := math.Abs(lt[b] - lT); d < bestD {
			best, bestD = b, d
		}
	}
	step := 0.0215 // octaves per index (both tables)
	if e.hasPrev && e.Opt.PitchHysteresis > 0 && e.prevB0 != best {
		dPrev := math.Abs(lt[e.prevB0] - lT)
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
	// Per codebook band j: cost if the band is decoded voiced (W1) or
	// unvoiced (W0). Exactly Σ wt·|vuv−V| regrouped by band.
	var W0, W1 [8]float64
	for l := 1; l <= L; l++ {
		jl := int(float32(l) * 16 * float32(f0))
		W1[jl] += wt[l] * (1 - V[l])
		W0[jl] += wt[l] * V[l]
	}
	var costs [32]float64
	bb := 0
	for b1 := 0; b1 < nVuv; b1++ {
		row := &AmbeVuv[b1]
		if e.mode == ambe.DStar {
			row = &AmbePlusVuv[b1]
		}
		var c float64
		for j := 0; j < 8; j++ {
			if row[j] != 0 {
				c += W1[j]
			} else {
				c += W0[j]
			}
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
				s += R[l0+j-1] * cosJd[J][k][j]
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

const maxTopK = 32

// topK keeps the K smallest (err, idx) pairs, sorted ascending.
type topK struct {
	n, k int
	idx  [maxTopK]int
	err  [maxTopK]float64
}

func (t *topK) init(k int) { t.n, t.k = 0, k }

func (t *topK) offer(i int, e float64) {
	if t.n == t.k && e >= t.err[t.n-1] {
		return
	}
	p := t.n
	if t.n < t.k {
		t.n++
	} else {
		p = t.k - 1
	}
	for p > 0 && t.err[p-1] > e {
		t.err[p], t.idx[p] = t.err[p-1], t.idx[p-1]
		p--
	}
	t.err[p], t.idx[p] = e, i
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
			G[m] += R8[i] * cos8d[m][i]
		}
		G[m] /= 8
	}
	// Linear map ΔG(7) -> v = [ΔC1_1..4, ΔC2_1..4]; error = vᵀDv - (jᵀv)²/L.
	var A [9][9]float64 // A[i][m] : ΔR8_i per ΔG_m
	for i := 1; i <= 8; i++ {
		for m := 2; m <= 8; m++ {
			A[i][m] = 2 * cos8d[m][i]
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
	// Top-K preselection per codebook on its own sub-quadratic form, then
	// exact joint evaluation. Fixed-size arrays: no allocation, no sorting.
	K := max(1, min(e.Opt.TopK, maxTopK))
	var top3, top4 topK
	top3.init(K)
	top4.init(K)
	var d [9]float64
	for n := range e.cb.prba24 {
		v := &e.cb.prba24[n]
		d[2], d[3], d[4] = float64(v[0])-G[2], float64(v[1])-G[3], float64(v[2])-G[4]
		top3.offer(n, quad(&d, 2, 4))
	}
	for n := range e.cb.prba58 {
		v := &e.cb.prba58[n]
		d[5], d[6], d[7], d[8] = float64(v[0])-G[5], float64(v[1])-G[6], float64(v[2])-G[7], float64(v[3])-G[8]
		top4.offer(n, quad(&d, 5, 8))
	}
	// Joint error = e3 + e4 + 2·d3ᵀ Q34 d4 (Q symmetric). Precompute
	// w = Q34ᵀ d3 per PRBA24 candidate so each pair costs 4 multiplies.
	var d4s [maxTopK][4]float64
	for b := 0; b < top4.n; b++ {
		v4 := &e.cb.prba58[top4.idx[b]]
		for k := 0; k < 4; k++ {
			d4s[b][k] = float64(v4[k]) - G[5+k]
		}
	}
	b3, b4, be := top3.idx[0], top4.idx[0], math.Inf(1)
	for a := 0; a < top3.n; a++ {
		v3 := &e.cb.prba24[top3.idx[a]]
		d3 := [3]float64{float64(v3[0]) - G[2], float64(v3[1]) - G[3], float64(v3[2]) - G[4]}
		var w [4]float64
		for k := 0; k < 4; k++ {
			w[k] = d3[0]*Q[2][5+k] + d3[1]*Q[3][5+k] + d3[2]*Q[4][5+k]
		}
		for b := 0; b < top4.n; b++ {
			x := &d4s[b]
			v := top3.err[a] + top4.err[b] + 2*(w[0]*x[0]+w[1]*x[1]+w[2]*x[2]+w[3]*x[3])
			if v < be {
				b3, b4, be = top3.idx[a], top4.idx[b], v
			}
		}
	}
	return b3, b4
}
