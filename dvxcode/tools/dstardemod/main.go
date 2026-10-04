// dstardemod recovers D-STAR AMBE voice frames from FM discriminator audio
// (e.g. a scanner recording). Output: one 9-byte frame per line (hex, network
// byte order) plus FEC statistics on stderr.
//
//	ffmpeg -i rec.mp3 -ac 1 -ar 48000 -sample_fmt s16 rec.wav
//	dstardemod -in rec.wav > frames.hex
//
// Method: matched-filter slicing, 24-bit voice-sync search on all sample
// phases and both polarities, then FEC-aided timing: each frame's start is
// refined within ±3 samples by minimising Golay corrections.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"sort"

	"github.com/2E0LXY/dvxcode/ambe"
	"github.com/2E0LXY/dvxcode/internal/wav"
)

const baud = 4800

// Voice-frame sync (slow-data bytes 0x55 0x2D 0x16, LSB-first on air).
var syncBits = bitsLSB([]byte{0x55, 0x2D, 0x16})

// End-of-transmission tail: 0x55 0x55 0x55 0x55 0xC8 0x7A after 32-bit preamble.
var eotBits = bitsLSB([]byte{0x55, 0xC8, 0x7A})

func bitsLSB(b []byte) []int8 {
	out := make([]int8, 0, 8*len(b))
	for _, v := range b {
		for i := 0; i < 8; i++ {
			out = append(out, int8(v>>i)&1)
		}
	}
	return out
}

type demod struct {
	y   []float64 // filtered soft signal
	sps float64   // samples per bit
	pol float64
}

// soft returns the integrated soft value of the bit starting at sample s.
func (d *demod) soft(s float64) float64 {
	a := int(math.Round(s + d.sps*0.25))
	b := int(math.Round(s + d.sps*0.75))
	if a < 0 || b >= len(d.y) {
		return 0
	}
	var v float64
	for i := a; i <= b; i++ {
		v += d.y[i]
	}
	return v * d.pol
}

func (d *demod) bit(s float64) int8 {
	if d.soft(s) > 0 {
		return 1
	}
	return 0
}

func (d *demod) matches(s float64, pat []int8) int {
	n := 0
	for i, p := range pat {
		if d.bit(s+float64(i)*d.sps) == p {
			n++
		}
	}
	return n
}

func (d *demod) frame(s float64) ([9]byte, ambe.FECStats) {
	var f [9]byte
	for i := 0; i < 72; i++ {
		f[i>>3] |= byte(d.bit(s+float64(i)*d.sps)) << (i & 7)
	}
	_, st := ambe.Decode(ambe.DStar, f)
	return f, st
}

func cost(st ambe.FECStats) int {
	c := st.Total()
	if !st.C0Parity {
		c++
	}
	if !st.C1Parity {
		c++
	}
	return c
}

func main() {
	in := flag.String("in", "", "48 kHz (or any multiple of 4800) mono WAV")
	minSync := flag.Int("sync", 23, "minimum matching bits of the 24-bit sync")
	flag.Parse()
	x, rate, err := wav.Read(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sps := float64(rate) / baud
	// DC/baseline removal (≈20 ms window) then normalise.
	y := make([]float64, len(x))
	win := int(sps * 96)
	var acc float64
	for i := range x {
		acc += x[i]
		if i >= win {
			acc -= x[i-win]
		}
		y[i] = x[i] - acc/float64(min(i+1, win))
	}

	best := struct {
		pol  float64
		hits []float64
	}{}
	for _, pol := range []float64{1, -1} {
		d := &demod{y: y, sps: sps, pol: pol}
		var hits []float64
		for s := 0.0; s+24*sps < float64(len(y)); s++ {
			if d.matches(s, syncBits) >= *minSync {
				if len(hits) > 0 && s-hits[len(hits)-1] < 10*sps {
					continue
				}
				hits = append(hits, s)
			}
		}
		if len(hits) > len(best.hits) {
			best.pol, best.hits = pol, hits
		}
	}
	d := &demod{y: y, sps: sps, pol: best.pol}
	fmt.Fprintf(os.Stderr, "polarity %+.0f, %d sync hits\n", best.pol, len(best.hits))
	if len(best.hits) == 0 {
		os.Exit(2)
	}

	// Walk forward from each sync, frame by frame. Frame start = sync - 72 bits;
	// voice for the sync frame itself begins 72 bits before its sync data.
	type rec struct {
		start float64
		f     [9]byte
		st    ambe.FECStats
		sync  bool
	}
	var frames []rec
	seen := map[int]bool{}
	for _, h := range best.hits {
		s := h - 72*sps
		misses := 0
		for n := 0; s+96*sps < float64(len(y)); n++ {
			key := int(s / (48 * sps))
			if seen[key] {
				break
			}
			// FEC-aided timing refinement.
			bestC, bestS := 1<<30, s
			var bf [9]byte
			var bst ambe.FECStats
			for off := -3.0; off <= 3; off++ {
				f, st := d.frame(s + off)
				c := cost(st)
				if c < bestC || (c == bestC && math.Abs(off) < math.Abs(bestS-s)) {
					bestC, bestS, bf, bst = c, s+off, f, st
				}
			}
			s = bestS
			isSync := d.matches(s+72*sps, syncBits) >= 22
			if d.matches(s, eotBits) >= 23 {
				break
			}
			if n%21 == 0 && !isSync {
				misses++
			} else if isSync {
				misses = 0
			}
			if misses >= 2 || bestC >= 7 && !isSync {
				break
			}
			seen[key] = true
			frames = append(frames, rec{s, bf, bst, isSync})
			s += 96 * sps
		}
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].start < frames[j].start })

	var tot, clean, bad int
	hist := make([]int, 8)
	for _, r := range frames {
		c := cost(r.st)
		tot += c
		if c == 0 {
			clean++
		}
		if c >= 3 {
			bad++
		}
		hist[min(c, 7)]++
		fmt.Printf("%x # t=%.3f c0=%d c1=%d p0=%t p1=%t%s\n", r.f, r.start/float64(rate), r.st.C0, r.st.C1, r.st.C0Parity, r.st.C1Parity,
			map[bool]string{true: " sync", false: ""}[r.sync])
	}
	fmt.Fprintf(os.Stderr, "frames %d (%.2f s), FEC-clean %d, cost>=3 %d, mean corrected bits/frame %.2f\n",
		len(frames), float64(len(frames))*0.02, clean, bad, float64(tot)/float64(max(1, len(frames))))
	fmt.Fprintf(os.Stderr, "cost histogram 0..7+: %v\n", hist)
}
