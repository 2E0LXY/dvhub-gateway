// Package xcode transcodes AMBE voice between D-STAR (3600x2400) and the
// DMR family (AMBE+2 3600x2450) in the MBE parameter domain: no PCM, no
// re-analysis, closed-loop re-quantisation.
package xcode

import (
	"math"

	"github.com/2E0LXY/dvxcode/ambe"
	"github.com/2E0LXY/dvxcode/mbe"
)

// Options control concealment and anti-warble behaviour.
type Options struct {
	// Conceal replaces erased or FEC-unreliable frames with a decaying
	// repeat of the last good frame instead of passing corrupted parameters.
	Conceal bool
	// Lookahead delays output by one frame (20 ms) so isolated pitch
	// outliers (octave errors) can be repaired from both neighbours.
	Lookahead bool
	// MaxRepeats bounds concealment of bad/erased frames before muting.
	MaxRepeats int
	// RepeatDecay is the gain reduction per concealed frame (log2 units; 0.5 = 3 dB).
	RepeatDecay float32
	// Encoder tuning for the target codec.
	Encoder mbe.EncoderOptions
}

// DefaultOptions favour stability over minimum latency.
var DefaultOptions = Options{Conceal: true, Lookahead: true, MaxRepeats: 3, RepeatDecay: 0.5, Encoder: mbe.DefaultEncoderOptions}

// Stats counts per-stream events.
type Stats struct {
	Frames, Voice, Silence, Tone, Erasure int
	BadFEC, Concealed, Muted              int
	PartialConceal, Interpolated          int
	PitchRepairs                          int
}

type item struct {
	p         mbe.Params
	silence   bool
	concealed bool // whole frame repeated from history; refine with lookahead
}

// Transcoder converts one voice stream. Not safe for concurrent use; create
// one per call/stream and call Reset at stream start.
type Transcoder struct {
	src, dst ambe.Mode
	dec      *mbe.Decoder
	enc      *mbe.Encoder
	srcEnc   *mbe.Encoder // only for null-frame detection
	opt      Options
	last     mbe.Params
	hasLast  bool
	repeats  int
	q        []item // lookahead window (max 3)
	Stats    Stats
}

// New returns a transcoder from src to dst.
func New(src, dst ambe.Mode, opt Options) *Transcoder {
	t := &Transcoder{src: src, dst: dst, opt: opt}
	t.Reset()
	return t
}

// Reset prepares for a new transmission.
func (t *Transcoder) Reset() {
	t.dec = mbe.NewDecoder(t.src)
	t.enc = mbe.NewEncoder(t.dst)
	t.enc.Opt = t.opt.Encoder
	t.srcEnc = mbe.NewEncoder(t.src)
	t.hasLast, t.repeats, t.q = false, 0, t.q[:0]
}

// Target returns the receiver-side parameters of the last emitted frame.
func (t *Transcoder) Target() mbe.Params { return t.enc.Shadow() }

// EncoderStats exposes target quantiser diagnostics.
func (t *Transcoder) EncoderStats() mbe.EncoderStats { return t.enc.Stats }

// c0ok reports whether the first Golay word (pitch MSBs, part of V/UV and
// gain) can be trusted even when the rest of the frame cannot.
func c0ok(st ambe.FECStats) bool { return st.C0Parity }

// reliable reports whether the protected parameter bits can be trusted.
// Golay(24,12) corrects 3 errors and, via the extended parity, detects 4:
// a parity failure means the pitch/voicing word is probably wrong, which is
// exactly what produces "R2-D2" warble if passed on. Up to 3 corrections
// with consistent parity are trusted.
func reliable(m ambe.Mode, st ambe.FECStats) bool {
	if !st.C0Parity {
		return false
	}
	if m == ambe.DStar && !st.C1Parity {
		return false
	}
	if m == ambe.DMR && st.C0 == 3 && st.C1 == 3 { // both words saturated, no parity on C1
		return false
	}
	return true
}

// decodeOne turns a source frame into a target-side item (pre-smoothing).
func (t *Transcoder) decodeOne(in [9]byte) item {
	t.Stats.Frames++
	bits, st := ambe.Decode(t.src, in)
	snap := t.dec.Snapshot()
	p, k := t.dec.Decode(bits)

	if k == mbe.Voice && t.srcEnc.IsNull(bits) {
		k = mbe.Silence
	}
	good := reliable(t.src, st)
	if !t.opt.Conceal && k != mbe.Erasure {
		good = true // pass-through: whatever the source decoder produced
	}
	if !reliable(t.src, st) {
		t.Stats.BadFEC++
	}
	switch {
	case k == mbe.Silence && good:
		t.Stats.Silence++
		t.hasLast, t.repeats = false, 0
		return item{silence: true}
	case k == mbe.Tone && good:
		// Tone/DTMF translation is not implemented: mute rather than emit garbage.
		t.Stats.Tone++
		t.dec.Restore(snap)
		t.hasLast = false
		return item{silence: true}
	case k == mbe.Voice && good:
		t.Stats.Voice++
		t.last, t.hasLast, t.repeats = p, true, 0
		return item{p: p}
	}
	// Erasure or unreliable: never pass corrupted parameters on (the
	// classic "R2-D2" warble). Roll the source predictor back and conceal.
	if k == mbe.Erasure {
		t.Stats.Erasure++
	}
	t.dec.Restore(snap)
	// Partial concealment (D-STAR): C0 good, C1 parity failed. Pitch, voicing
	// and gain are trustworthy; only the spectral shape bits are suspect, so
	// keep the new pitch/voicing/level and borrow the last good shape.
	if k == mbe.Voice && t.src == ambe.DStar && c0ok(st) && t.hasLast {
		t.dec.Decode(bits) // advance the gain predictor with the trusted gain bits
		q := mbe.Resample(t.last, p.W0)
		q.Gamma = p.Gamma
		shift := p.MeanLog2() - q.MeanLog2()
		for l := 1; l <= q.L; l++ {
			q.Log2Ml[l] += shift
			if l <= p.L {
				q.Vl[l] = p.Vl[l]
			}
		}
		t.dec.SetPrev(q)
		t.last, t.repeats = q, 0
		t.Stats.PartialConceal++
		return item{p: q}
	}
	if t.hasLast && t.repeats < t.opt.MaxRepeats {
		t.repeats++
		t.Stats.Concealed++
		c := t.last
		for l := 1; l <= c.L; l++ {
			c.Log2Ml[l] -= t.opt.RepeatDecay * float32(t.repeats)
		}
		t.dec.SetPrev(c)
		return item{p: c, concealed: true}
	}
	t.Stats.Muted++
	t.hasLast = false
	return item{silence: true}
}

func voiced(it item) bool { return !it.silence && it.p.VoicedFraction(1000) >= 0.5 }

// repair fixes an isolated pitch outlier between two agreeing neighbours, and
// replaces a concealed (repeated) frame by interpolating its neighbours so the
// pitch keeps moving instead of stalling.
func (t *Transcoder) repair(a, b, c *item) {
	if b.concealed && !a.silence && !c.silence && !c.concealed {
		b.p = mbe.Interpolate(a.p, c.p)
		b.concealed = false
		t.Stats.Interpolated++
		return
	}
	if !voiced(*a) || !voiced(*b) || !voiced(*c) {
		return
	}
	fa, fb, fc := float64(a.p.W0), float64(b.p.W0), float64(c.p.W0)
	if math.Abs(math.Log2(fa/fc)) > 0.2 { // neighbours must agree within ~15%
		return
	}
	m := math.Sqrt(fa * fc)
	if r := math.Abs(math.Log2(fb / m)); r > 0.7 { // outlier beyond ~1.6x
		b.p = mbe.Resample(b.p, float32(m))
		t.Stats.PitchRepairs++
	}
}

func (t *Transcoder) emit(it item) [9]byte {
	var bits ambe.Bits49
	if it.silence {
		bits = t.enc.EncodeSilence()
	} else {
		bits = t.enc.Encode(it.p)
	}
	return ambe.Encode(t.dst, bits)
}

// Frame transcodes one 20 ms frame. With Lookahead the returned frame is the
// previous input's (first call returns target silence).
func (t *Transcoder) Frame(in [9]byte) [9]byte {
	it := t.decodeOne(in)
	if !t.opt.Lookahead {
		return t.emit(it)
	}
	t.q = append(t.q, it)
	if len(t.q) == 1 {
		return t.emit(item{silence: true})
	}
	if len(t.q) == 3 {
		t.repair(&t.q[0], &t.q[1], &t.q[2])
		t.q = t.q[1:]
	}
	// Emit the middle (or first, at start) element: one frame of delay.
	return t.emit(t.q[len(t.q)-2])
}

// Lost conceals a frame that never arrived (network loss). Same timing as Frame.
func (t *Transcoder) Lost() [9]byte {
	t.Stats.Frames++
	t.Stats.BadFEC++
	var it item
	if t.hasLast && t.repeats < t.opt.MaxRepeats {
		t.repeats++
		t.Stats.Concealed++
		c := t.last
		for l := 1; l <= c.L; l++ {
			c.Log2Ml[l] -= t.opt.RepeatDecay * float32(t.repeats)
		}
		t.dec.SetPrev(c)
		it = item{p: c, concealed: true}
	} else {
		t.Stats.Muted++
		t.hasLast = false
		it = item{silence: true}
	}
	if !t.opt.Lookahead {
		return t.emit(it)
	}
	t.q = append(t.q, it)
	if len(t.q) == 1 {
		return t.emit(item{silence: true})
	}
	if len(t.q) == 3 {
		t.repair(&t.q[0], &t.q[1], &t.q[2])
		t.q = t.q[1:]
	}
	return t.emit(t.q[len(t.q)-2])
}

// Flush emits the frame held for lookahead (call at end of stream).
func (t *Transcoder) Flush() ([9]byte, bool) {
	if !t.opt.Lookahead || len(t.q) == 0 {
		return [9]byte{}, false
	}
	it := t.q[len(t.q)-1]
	t.q = t.q[:0]
	return t.emit(it), true
}
