// xcbench compares parametric transcoding against a tandem (decode -> PCM ->
// re-analyse -> encode) baseline, on real D-STAR captures and on DMR streams
// generated from speech, and writes WAV files for listening.
//
//	xcbench -dstar testdata/vectors/dstar_real.hex -out bench_out
package main

import (
	"bufio"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/2E0LXY/dvxcode/ambe"
	"github.com/2E0LXY/dvxcode/internal/wav"
	"github.com/2E0LXY/dvxcode/mbe"
	"github.com/2E0LXY/dvxcode/xcode"
)

func loadHex(path string) ([][9]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out [][9]byte
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		b, err := hex.DecodeString(line)
		if err != nil || len(b) != 9 {
			return nil, fmt.Errorf("bad line %q", line)
		}
		var fr [9]byte
		copy(fr[:], b)
		out = append(out, fr)
	}
	return out, s.Err()
}

// decodeStream decodes frames to evaluation params (silence/tone => Silent).
func decodeStream(m ambe.Mode, frames [][9]byte) []frameP {
	d := mbe.NewDecoder(m)
	null := mbe.NewEncoder(m)
	out := make([]frameP, len(frames))
	for i, f := range frames {
		bits, _ := ambe.Decode(m, f)
		p, k := d.Decode(bits)
		out[i] = frameP{P: p, Silent: k != mbe.Voice || null.IsNull(bits)}
	}
	return out
}

func render(fp []frameP) []float64 {
	s := mbe.NewSynth(7)
	var y []float64
	for _, f := range fp {
		y = append(y, s.Frame(f.P, f.Silent)...)
	}
	return y
}

// encodeAnalysed quantises analysed PCM into a target stream.
func encodeAnalysed(m ambe.Mode, x []float64) [][9]byte {
	fr := mbe.Analyse(x)
	enc := mbe.NewEncoder(m)
	out := make([][9]byte, len(fr))
	for i, f := range fr {
		var b ambe.Bits49
		if f.Kind == mbe.Voice {
			b = enc.Encode(f.P)
		} else {
			b = enc.EncodeSilence()
		}
		out[i] = ambe.Encode(m, b)
	}
	return out
}

func parametric(src, dst ambe.Mode, in [][9]byte, opt xcode.Options) ([][9]byte, xcode.Stats, time.Duration) {
	t := xcode.New(src, dst, opt)
	out := make([][9]byte, 0, len(in))
	start := time.Now()
	for _, f := range in {
		out = append(out, t.Frame(f))
	}
	if f, ok := t.Flush(); ok {
		out = append(out, f)
	}
	el := time.Since(start)
	if opt.Lookahead && len(out) > 0 {
		out = out[1:] // remove the one-frame lookahead delay for alignment
	}
	return out, t.Stats, el / time.Duration(max(1, len(in)))
}

// addErrors flips each of the 72 channel bits with probability ber.
func addErrors(in [][9]byte, ber float64, seed int64) [][9]byte {
	r := rand.New(rand.NewSource(seed))
	out := make([][9]byte, len(in))
	for i, f := range in {
		for b := 0; b < 72; b++ {
			if r.Float64() < ber {
				f[b>>3] ^= 1 << (b & 7)
			}
		}
		out[i] = f
	}
	return out
}

func tandem(src, dst ambe.Mode, in [][9]byte) [][9]byte {
	return encodeAnalysed(dst, render(decodeStream(src, in)))
}

type row struct {
	name string
	m    Metrics
}

func printRows(title string, rows []row) {
	fmt.Printf("\n## %s\n\n", title)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "path\tf0 median ¢\tf0 err >50¢ %\toctave %\tV/UV agree %\tLSD dB\tjitter ref ¢\tjitter out ¢\tflux ref dB\tflux out dB\t")
	for _, r := range rows {
		m := r.m
		fmt.Fprintf(w, "%s\t%.1f\t%.2f\t%.2f\t%.1f\t%.2f\t%.1f\t%.1f\t%.2f\t%.2f\t\n", r.name, m.MedianCents, m.PitchErrPct, m.OctavePct,
			m.VUVAgreePct, m.LSDdB, m.JitterRef, m.JitterOut, m.FluxRef, m.FluxOut)
	}
	w.Flush()
}

func main() {
	dstarPath := flag.String("dstar", "testdata/vectors/dstar_real.hex", "D-STAR frames (hex)")
	outDir := flag.String("out", "bench_out", "directory for WAV output")
	hops := flag.Int("hops", 4, "multi-hop chain length")
	flag.Parse()
	must := func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	must(os.MkdirAll(*outDir, 0o755))
	ds, err := loadHex(*dstarPath)
	must(err)
	save := func(name string, fp []frameP) {
		must(wav.Write(filepath.Join(*outDir, name), render(fp), 8000))
	}

	fmt.Printf("source: %d real D-STAR frames (%.1f s)\n", len(ds), float64(len(ds))*0.02)

	// D-STAR -> DMR.
	ref := decodeStream(ambe.DStar, ds)
	save("1_dstar_source.wav", ref)
	pOut, st, perFrame := parametric(ambe.DStar, ambe.DMR, ds, xcode.DefaultOptions)
	noLA := xcode.DefaultOptions
	noLA.Lookahead = false
	pOut0, _, _ := parametric(ambe.DStar, ambe.DMR, ds, noLA)
	raw := noLA
	raw.Encoder.PitchHysteresis, raw.Encoder.VUVHysteresis = 0, 0
	pOutRaw, _, _ := parametric(ambe.DStar, ambe.DMR, ds, raw)
	tOut := tandem(ambe.DStar, ambe.DMR, ds)
	pDec := decodeStream(ambe.DMR, pOut)
	tDec := decodeStream(ambe.DMR, tOut)
	save("2_dstar_to_dmr_parametric.wav", pDec)
	save("3_dstar_to_dmr_tandem.wav", tDec)
	printRows("D-STAR → DMR (reference: decoded D-STAR source)", []row{
		{"parametric (lookahead+hysteresis)", compare(ref, pDec)},
		{"parametric (hysteresis, no lookahead)", compare(ref, decodeStream(ambe.DMR, pOut0))},
		{"parametric (no anti-warble)", compare(ref, decodeStream(ambe.DMR, pOutRaw))},
		{"tandem (synth → analyse → encode)", compare(ref, tDec)},
	})
	fmt.Printf("\nparametric stats: %+v\nper-frame cost: %v\n", st, perFrame)

	// Channel errors: random bit errors on the D-STAR source frames.
	fmt.Printf("\n## D-STAR → DMR with channel bit errors (reference: clean D-STAR decode)\n\n")
	w0 := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w0, "BER %\tpath\tbad FEC frames\tconcealed\tf0 err >50¢ %\toctave %\tLSD dB\tjitter out ¢ (clean ref)\t")
	pass := xcode.DefaultOptions
	pass.Conceal, pass.Lookahead = false, false
	pass.Encoder.PitchHysteresis, pass.Encoder.VUVHysteresis = 0, 0
	for _, ber := range []float64{0.01, 0.03, 0.05, 0.08} {
		noisy := addErrors(ds, ber, 99)
		for _, c := range []struct {
			name string
			o    xcode.Options
		}{{"pass-through (no anti-warble)", pass}, {"concealment + anti-warble", xcode.DefaultOptions}} {
			out, st, _ := parametric(ambe.DStar, ambe.DMR, noisy, c.o)
			dec := decodeStream(ambe.DMR, out)
			m := compare(ref, dec)
			fmt.Fprintf(w0, "%.0f\t%s\t%d\t%d\t%.2f\t%.2f\t%.2f\t%.1f (%.1f)\t\n", ber*100, c.name, st.BadFEC, st.Concealed, m.PitchErrPct, m.OctavePct, m.LSDdB, m.JitterOut, m.JitterRef)
			if ber == 0.08 {
				tag := "pass"
				if c.o.Conceal {
					tag = "conceal"
				}
				save(fmt.Sprintf("9_ber8_%s.wav", tag), dec)
			}
		}
	}
	w0.Flush()

	// DMR -> D-STAR: DMR source generated from the decoded speech.
	speech := render(ref)
	dm := encodeAnalysed(ambe.DMR, speech)
	ref2 := decodeStream(ambe.DMR, dm)
	save("4_dmr_source.wav", ref2)
	p2, _, _ := parametric(ambe.DMR, ambe.DStar, dm, xcode.DefaultOptions)
	t2 := tandem(ambe.DMR, ambe.DStar, dm)
	p2d, t2d := decodeStream(ambe.DStar, p2), decodeStream(ambe.DStar, t2)
	save("5_dmr_to_dstar_parametric.wav", p2d)
	save("6_dmr_to_dstar_tandem.wav", t2d)
	printRows("DMR → D-STAR (reference: decoded DMR source)", []row{
		{"parametric", compare(ref2, p2d)},
		{"tandem", compare(ref2, t2d)},
	})

	// Multi-hop chain D→M→D→M… versus the original D-STAR.
	fmt.Printf("\n## Multi-hop D-STAR ⇄ DMR (reference: original D-STAR)\n\n")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "hops\tparam LSD dB\tparam f0 >50¢ %\ttandem LSD dB\ttandem f0 >50¢ %\t")
	pc, tc := ds, ds
	modes := []ambe.Mode{ambe.DStar, ambe.DMR}
	for h := 1; h <= *hops; h++ {
		s, d := modes[(h-1)%2], modes[h%2]
		pc, _, _ = parametric(s, d, pc, xcode.DefaultOptions)
		tc = tandem(s, d, tc)
		pm := compare(ref, decodeStream(d, pc))
		tm := compare(ref, decodeStream(d, tc))
		fmt.Fprintf(w, "%d\t%.2f\t%.2f\t%.2f\t%.2f\t\n", h, pm.LSDdB, pm.PitchErrPct, tm.LSDdB, tm.PitchErrPct)
		if h == *hops {
			save(fmt.Sprintf("7_%dhop_parametric.wav", h), decodeStream(d, pc))
			save(fmt.Sprintf("8_%dhop_tandem.wav", h), decodeStream(d, tc))
		}
	}
	w.Flush()
}
