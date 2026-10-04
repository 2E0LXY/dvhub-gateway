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
	// SuspectCheck verifies frames whose FEC is near its limit against their
	// neighbours (requires Lookahead) and fully conceals rather than partially
	// conceals when the pitch word needed 3 corrections.
	SuspectCheck bool
	// LookaheadFrames is the lookahead depth (1..4, default 2 = 40 ms).
	// Deeper windows let concealment interpolate across longer error bursts.
	LookaheadFrames int
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
var DefaultOptions = Options{Conceal: true, SuspectCheck: true, Lookahead: true, LookaheadFrames: 2, MaxRepeats: 3, RepeatDecay: 0.5, Encoder: mbe.DefaultEncoderOptions}

// Stats counts per-stream events.
type Stats struct {
	Frames, Voice, Silence, Tone, Erasure int
	BadFEC, Concealed, Muted              int
	PartialConceal, Interpolated          int
	SuspectRepairs                        int
	PitchRepairs                          int
}

type item struct {
	p         mbe.Params
	silence   bool
	concealed bool // whole frame repeated from history; refine with lookahead
	suspect   bool // FEC near its limit: pitch may be a miscorrection; verify against neighbours
	tag       byte // provenance for diagnostics: g good, s suspect, p partial, c concealed, i interpolated, r repaired, m muted
}

// Transcoder converts one voice stream. Not safe for concurrent use; create
// one per call/stream and call Reset at stream start.
type Transcoder struct {
	src, dst   ambe.Mode
	dec        *mbe.Decoder
	enc        *mbe.Encoder
	srcEnc     *mbe.Encoder // only for null-frame detection
	opt        Options
	last       mbe.Params
	hasLast    bool
	repeats    int
	q          []item  // frames waiting in the lookahead window
	prevOut    item    // last emitted (after repair)
	trace      *[]byte // optional provenance trace (tests/diagnostics)
	trusted    item    // last emitted frame known to be good
	hasTrusted bool
	trustAge   int // frames emitted since trusted
	hasPrevOut bool
	Stats      Stats
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
	t.hasLast, t.repeats, t.q, t.hasPrevOut, t.hasTrusted = false, 0, t.q[:0], false, false
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
	// At high BER a Golay word that needed all 3 corrections is often a
	// miscorrection of 5+ errors (measured: ~25-50% wrong pitch MSBs when
	// the rest of the frame is also damaged). Such frames are kept but
	// marked so the lookahead can check them against their neighbours.
	suspect := t.opt.Conceal && t.opt.SuspectCheck && (st.C0 >= 3 || (st.C0 >= 2 && (st.C1 >= 3 || !st.C1Parity)))
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
		tg := byte('g')
		if suspect {
			tg = 's'
		}
		return item{p: p, suspect: suspect, tag: tg}
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
	if k == mbe.Voice && t.src == ambe.DStar && c0ok(st) && (st.C0 < 3 || !t.opt.SuspectCheck) && t.hasLast {
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
		return item{p: q, suspect: st.C0 >= 2, tag: 'p'}
	}
	if t.hasLast && t.repeats < t.opt.MaxRepeats {
		t.repeats++
		t.Stats.Concealed++
		c := t.last
		for l := 1; l <= c.L; l++ {
			c.Log2Ml[l] -= t.opt.RepeatDecay * float32(t.repeats)
		}
		t.dec.SetPrev(c)
		return item{p: c, concealed: true, tag: 'c'}
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

// depth returns the lookahead depth in frames (0 when disabled).
func (t *Transcoder) depth() int {
	if !t.opt.Lookahead {
		return 0
	}
	return max(1, min(t.opt.LookaheadFrames, 4))
}

// push queues an item and emits the frame that leaves the lookahead window.
func (t *Transcoder) push(it item) [9]byte {
	D := t.depth()
	if D == 0 {
		return t.emit(it)
	}
	t.q = append(t.q, it)
	if len(t.q) <= D {
		return t.emit(item{silence: true}) // start-up delay
	}
	return t.release()
}

// release finalises and emits the oldest queued item.
func (t *Transcoder) release() [9]byte {
	b := t.q[0]
	t.finish(&b, t.q[1:])
	t.q = append(t.q[:0], t.q[1:]...)
	t.prevOut, t.hasPrevOut = b, true
	switch {
	case b.silence:
		t.hasTrusted = false
	case !b.suspect && !b.concealed:
		t.trusted, t.hasTrusted, t.trustAge = b, true, 0
	default:
		t.trustAge++
	}
	if t.trace != nil {
		*t.trace = append(*t.trace, b.tag)
	}
	return t.emit(b)
}

// anchorAhead returns the first fully trustworthy frame in ahead (not
// silence, concealed or FEC-suspect). Failing that, under heavy errors, it
// accepts a suspect frame whose pitch is plausible relative to a (speech
// pitch moves at most ~0.25 octave per frame).
func anchorAhead(a *item, k float64, ahead []item) (int, bool) {
	for pass := 0; pass < 2; pass++ {
		for j := range ahead {
			c := &ahead[j]
			if c.silence {
				break
			}
			if c.concealed {
				continue
			}
			if !c.suspect {
				return j, true
			}
			if pass == 1 && voiced(*a) && voiced(*c) &&
				math.Abs(math.Log2(float64(c.p.W0)/float64(a.p.W0))) <= 0.25*(k+float64(j+1)) {
				return j, true
			}
		}
	}
	return 0, false
}

// interp is InterpolateAt, but pitch only comes from voiced endpoints:
// an unvoiced frame's fundamental is arbitrary and must not steer pitch.
func interp(a, c *item, frac float64) mbe.Params {
	q := mbe.InterpolateAt(a.p, c.p, frac)
	va, vc := voiced(*a), voiced(*c)
	switch {
	case va && !vc:
		q = mbe.Resample(q, a.p.W0)
	case vc && !va:
		q = mbe.Resample(q, c.p.W0)
	}
	return q
}

// finish applies lookahead repairs to b. References are the last trusted
// emitted frame (never an unverified suspect one, so errors cannot cascade)
// and the next trusted frame in the lookahead window.
func (t *Transcoder) finish(b *item, ahead []item) {
	if b.silence || !t.hasTrusted || t.trustAge > 6 {
		return
	}
	a := &t.trusted
	k := float64(t.trustAge + 1) // distance a -> b in frames
	j, ok := anchorAhead(a, k, ahead)
	frac := k / (k + float64(j+1))
	switch {
	case b.concealed:
		if ok {
			b.p = interp(a, &ahead[j], frac)
			b.concealed, b.tag = false, 'i'
			t.Stats.Interpolated++
		}
		return
	case b.suspect:
		if !voiced(*a) {
			return
		}
		if ok && voiced(ahead[j]) {
			want := math.Exp2((1-frac)*math.Log2(float64(a.p.W0)) + frac*math.Log2(float64(ahead[j].p.W0)))
			if math.Abs(math.Log2(float64(b.p.W0)/want)) > suspectTol {
				b.p = mbe.InterpolateAt(a.p, ahead[j].p, frac)
				b.suspect, b.tag = false, 'r'
				t.Stats.SuspectRepairs++
			} else {
				b.suspect = false // verified
			}
		} else if voiced(*b) && math.Abs(math.Log2(float64(b.p.W0)/float64(a.p.W0))) > 0.4*k {
			b.p = mbe.Resample(b.p, a.p.W0)
			b.suspect, b.tag = false, 'r'
			t.Stats.SuspectRepairs++
		}
		return
	}
	if len(ahead) > 0 && t.trustAge == 0 {
		t.repair(a, b, &ahead[0])
	}
}

// Frame transcodes one 20 ms frame. With Lookahead the returned frame is
// delayed by the lookahead depth (start-up frames are silence).
func (t *Transcoder) Frame(in [9]byte) [9]byte { return t.push(t.decodeOne(in)) }

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
	return t.push(it)
}

// Flush emits every frame still held for lookahead (call at end of stream).
func (t *Transcoder) Flush() [][9]byte {
	var out [][9]byte
	for len(t.q) > 0 {
		out = append(out, t.release())
	}
	return out
}

// suspectTol is the pitch deviation (octaves) from the expected trajectory
// beyond which a FEC-suspect frame is treated as a miscorrection.
var suspectTol = 0.15
