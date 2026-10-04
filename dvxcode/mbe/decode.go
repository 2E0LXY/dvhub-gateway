package mbe

import (
	"math"

	"github.com/2E0LXY/dvxcode/ambe"
)

// Decoder holds inter-frame prediction state. Not safe for concurrent use.
type Decoder struct {
	mode      ambe.Mode
	cur, prev Params
}

// NewDecoder returns a decoder initialised as mbe_initMbeParms.
func NewDecoder(m ambe.Mode) *Decoder {
	d := &Decoder{mode: m}
	d.prev.W0 = 0.09378
	d.prev.L = 30
	d.cur = d.prev
	return d
}

// Prev exposes the prediction state (the last accepted frame).
func (d *Decoder) Prev() Params { return d.prev }

// Decode dequantises one frame. For Voice and Silence the returned Params
// are valid and become the prediction state; Erasure and Tone leave state
// unchanged (caller decides on repeat/mute).
func (d *Decoder) Decode(bits ambe.Bits49) (Params, Kind) {
	var ix Indices
	if d.mode == ambe.DMR {
		ix = IndicesDMR(&bits)
	} else {
		ix = IndicesDStar(&bits)
	}
	k := d.decode(ix)
	if k == Voice || k == Silence {
		d.prev = d.cur
	}
	return d.cur, k
}

type codebooks struct {
	ltable                 []float32
	lmprbl                 *[57][4]int8
	dg                     []float32
	prba24                 *[512][3]float32
	prba58                 *[128][4]float32
	hoc5, hoc6, hoc7, hoc8 [][4]float32
}

var cbDMR = codebooks{AmbeLtable[:], &AmbeLmprbl, AmbeDg[:], &AmbePRBA24, &AmbePRBA58,
	AmbeHOCb5[:], AmbeHOCb6[:], AmbeHOCb7[:], AmbeHOCb8[:]}
var cbDStar = codebooks{AmbePlusLtable[:], &AmbePlusLmprbl, AmbePlusDg[:], &AmbePlusPRBA24, &AmbePlusPRBA58,
	AmbePlusHOCb5[:], AmbePlusHOCb6[:], AmbePlusHOCb7[:], AmbePlusHOCb8[:]}

func cosf(x float64) float32 { return float32(math.Cos(float64(float32(x)))) }

func (d *Decoder) decode(ix Indices) Kind {
	cur, prev := &d.cur, &d.prev
	cb := &cbDMR
	if d.mode == ambe.DStar {
		cb = &cbDStar
	}
	kind := Voice
	var f0 float32
	var L int

	if d.mode == ambe.DMR {
		switch {
		case ix.B0 >= 120 && ix.B0 <= 123:
			return Erasure
		case ix.B0 == 124 || ix.B0 == 125:
			kind = Silence
			cur.W0 = float32(2 * math.Pi / 32)
			f0 = 1.0 / 32
			L = 14
			cur.L = L
			for l := 1; l <= L; l++ {
				cur.Vl[l] = 0
			}
		case ix.B0 >= 126:
			return Tone
		default:
			f0 = AmbeW0table[ix.B0]
			cur.W0 = f0 * float32(2*math.Pi)
			L = int(cb.ltable[ix.B0])
			cur.L = L
		}
	} else {
		if ix.B0&0x7E == 0x7E {
			return Tone
		}
		// mbelib "w0 guess" for D-STAR; no published table.
		f0 = float32(math.Pow(2, float64(float32(-4.311767578125-2.1336e-2*(float64(ix.B0)+0.5)))))
		cur.W0 = f0 * float32(2*math.Pi)
		L = int(cb.ltable[ix.B0])
		cur.L = L
	}

	// V/UV
	if kind != Silence {
		for l := 1; l <= L; l++ {
			jl := int(float32(l) * 16 * f0)
			if d.mode == ambe.DMR {
				cur.Vl[l] = AmbeVuv[ix.B1][jl]
			} else {
				cur.Vl[l] = AmbePlusVuv[ix.B1][jl]
			}
		}
	}

	// Gain
	cur.Gamma = cb.dg[ix.B2] + 0.5*prev.Gamma

	// PRBA
	var Gm [9]float32
	Gm[2], Gm[3], Gm[4] = cb.prba24[ix.B3][0], cb.prba24[ix.B3][1], cb.prba24[ix.B3][2]
	for i := 0; i < 4; i++ {
		Gm[5+i] = cb.prba58[ix.B4][i]
	}
	var Ri [9]float32
	for i := 1; i <= 8; i++ {
		var sum float32
		for m := 1; m <= 8; m++ {
			am := float32(2)
			if m == 1 {
				am = 1
			}
			sum += am * Gm[m] * cosf(math.Pi*float64(float32(m-1))*(float64(i)-0.5)/8)
		}
		Ri[i] = sum
	}
	var Cik [5][18]float32
	rc := float32(1 / (2 * math.Sqrt2))
	for i := 1; i <= 4; i++ {
		Cik[i][1] = 0.5 * (Ri[2*i-1] + Ri[2*i])
		Cik[i][2] = rc * (Ri[2*i-1] - Ri[2*i])
	}

	// HOC
	var Ji [5]int
	for i := 0; i < 4; i++ {
		Ji[i+1] = int(cb.lmprbl[L][i])
	}
	hoc := [5][]([4]float32){nil, cb.hoc5, cb.hoc6, cb.hoc7, cb.hoc8}
	bi := [5]int{0, ix.B5, ix.B6, ix.B7, ix.B8}
	for i := 1; i <= 4; i++ {
		for k := 3; k <= Ji[i]; k++ {
			if k > 6 {
				Cik[i][k] = 0
			} else {
				Cik[i][k] = hoc[i][bi[i]][k-3]
			}
		}
	}

	// Inverse DCT per block -> Tl
	var Tl [MaxL + 1]float32
	l := 1
	for i := 1; i <= 4; i++ {
		ji := Ji[i]
		for j := 1; j <= ji; j++ {
			var sum float32
			for k := 1; k <= ji; k++ {
				ak := float32(2)
				if k == 1 {
					ak = 1
				}
				sum += ak * Cik[i][k] * cosf(math.Pi*float64(float32(k-1))*(float64(j)-0.5)/float64(ji))
			}
			Tl[l] = sum
			l++
		}
	}

	// Predict log2Ml from previous frame (rho = 0.65).
	if cur.L > prev.L {
		for l := prev.L + 1; l <= cur.L; l++ {
			prev.Log2Ml[l] = prev.Log2Ml[prev.L]
		}
	}
	prev.Log2Ml[0] = prev.Log2Ml[1]

	var intkl [MaxL + 1]int
	var deltal [MaxL + 1]float32
	var sum43 float32
	for l := 1; l <= cur.L; l++ {
		fl := (float32(prev.L) / float32(cur.L)) * float32(l)
		intkl[l] = int(fl)
		deltal[l] = fl - float32(intkl[l])
		sum43 += (1-deltal[l])*prev.Log2Ml[intkl[l]] + deltal[l]*prev.Log2Ml[intkl[l]+1]
	}
	sum43 = (0.65 / float32(cur.L)) * sum43
	var sum42 float32
	for l := 1; l <= cur.L; l++ {
		sum42 += Tl[l]
	}
	sum42 /= float32(cur.L)
	bigGamma := cur.Gamma - float32(0.5*(math.Log(float64(float32(cur.L)))/math.Log(2))) - sum42
	for l := 1; l <= cur.L; l++ {
		c1 := 0.65 * (1 - deltal[l]) * prev.Log2Ml[intkl[l]]
		c2 := 0.65 * deltal[l] * prev.Log2Ml[intkl[l]+1]
		cur.Log2Ml[l] = Tl[l] + c1 + c2 - sum43 + bigGamma
	}
	return kind
}

// Snapshot returns a copy of the decoder state (for rollback on bad frames).
func (d *Decoder) Snapshot() Decoder { return *d }

// Restore rolls the decoder back to a snapshot.
func (d *Decoder) Restore(s Decoder) { *d = s }

// SetPrev overrides the prediction state (used when concealing a frame).
func (d *Decoder) SetPrev(p Params) { d.prev, d.cur = p, p }
