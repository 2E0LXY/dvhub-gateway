// mbedump decodes captured D-STAR / DMR AMBE voice to MBE parameters (CSV)
// and reports FEC and pitch/voicing stability statistics.
//
//	mbedump -fmt pcap -in capture.pcap > params.csv
//	mbedump -fmt raw  -mode dstar -in frames.bin
//	mbedump -fmt hex  -mode dmr   -in bursts.txt
//
// pcap: recognises HomeBrew DMRD (DMR) and DSVT (DExtra/XLX, DPlus) voice.
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/2E0LXY/dvxcode/ambe"
	"github.com/2E0LXY/dvxcode/mbe"
)

type stream struct {
	key     string
	mode    ambe.Mode
	dec     *mbe.Decoder
	frames  int
	kinds   [5]int
	fecBits int
	fecBad  int // frames with any correction or C0 parity failure
	lastF0  float64
	octave  int // consecutive voice frames with f0 ratio >1.8 or <0.55
	bigStep int // consecutive voice frames with f0 ratio outside 0.8..1.25
	vuvFlip int // harmonics changing V/UV between consecutive voice frames
	vuvTot  int
	lastVl  []int8
}

type dumper struct {
	w       *bufio.Writer
	streams map[string]*stream
	order   []string
}

func (d *dumper) get(key string, m ambe.Mode) *stream {
	s, ok := d.streams[key]
	if !ok {
		s = &stream{key: key, mode: m, dec: mbe.NewDecoder(m)}
		d.streams[key] = s
		d.order = append(d.order, key)
	}
	return s
}

func (d *dumper) frame(s *stream, f [9]byte) {
	bits, st := ambe.Decode(s.mode, f)
	p, k := s.dec.Decode(bits)
	s.frames++
	s.kinds[k]++
	s.fecBits += st.Total()
	if st.Total() > 0 || !st.C0Parity {
		s.fecBad++
	}
	var ix mbe.Indices
	if s.mode == ambe.DMR {
		ix = mbe.IndicesDMR(&bits)
	} else {
		ix = mbe.IndicesDStar(&bits)
	}
	fmt.Fprintf(d.w, "%s,%d,%s,%s,%d,%d,%t,%d", s.key, s.frames-1, s.mode, k, st.C0, st.C1, st.C0Parity, ix.B0)
	if k != mbe.Voice && k != mbe.Silence {
		d.w.WriteString(",,,,,\n")
		s.lastF0, s.lastVl = 0, nil
		return
	}
	f0 := p.F0Hz()
	var vb strings.Builder
	for l := 1; l <= p.L; l++ {
		vb.WriteByte('0' + byte(p.Vl[l]))
	}
	mags := make([]string, p.L)
	for l := 1; l <= p.L; l++ {
		mags[l-1] = fmt.Sprintf("%.3f", p.Log2Ml[l])
	}
	fmt.Fprintf(d.w, ",%.2f,%d,%.4f,%s,%s\n", f0, p.L, p.Gamma, vb.String(), strings.Join(mags, ";"))

	if k == mbe.Voice {
		if s.lastF0 > 0 {
			r := f0 / s.lastF0
			if r > 1.8 || r < 0.55 {
				s.octave++
			}
			if r > 1.25 || r < 0.8 {
				s.bigStep++
			}
		}
		if s.lastVl != nil {
			n := min(p.L, len(s.lastVl))
			for l := 1; l <= n; l++ {
				s.vuvTot++
				// compare by relative frequency band, not raw index
				j := int(math.Round(float64(l) * float64(len(s.lastVl)) / float64(p.L)))
				j = max(1, min(j, len(s.lastVl)))
				if s.lastVl[j-1] != p.Vl[l] {
					s.vuvFlip++
				}
			}
		}
		s.lastF0 = f0
		s.lastVl = append(s.lastVl[:0], p.Vl[1:p.L+1]...)
	} else {
		s.lastF0, s.lastVl = 0, nil
	}
}

func main() {
	in := flag.String("in", "-", "input file (- = stdin)")
	format := flag.String("fmt", "pcap", "pcap | raw | hex")
	mode := flag.String("mode", "", "dstar | dmr (raw/hex only)")
	flag.Parse()

	var r io.Reader = os.Stdin
	if *in != "-" {
		f, err := os.Open(*in)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}
	d := &dumper{w: bufio.NewWriter(os.Stdout), streams: map[string]*stream{}}
	defer d.w.Flush()
	fmt.Fprintln(d.w, "stream,seq,mode,kind,fec_c0,fec_c1,c0_parity_ok,b0,f0_hz,L,gamma,vuv,log2Ml")

	m := ambe.DMR
	switch *mode {
	case "dstar":
		m = ambe.DStar
	case "dmr", "":
	default:
		fatal(fmt.Errorf("unknown -mode %q", *mode))
	}
	if *format != "pcap" && *mode == "" {
		fatal(fmt.Errorf("-mode required for -fmt %s", *format))
	}

	var err error
	switch *format {
	case "pcap":
		err = readPCAP(bufio.NewReader(r), d.udp)
	case "raw":
		err = d.raw(r, m)
	case "hex":
		err = d.hexLines(r, m)
	default:
		err = fmt.Errorf("unknown -fmt %q", *format)
	}
	if err != nil {
		fatal(err)
	}
	d.w.Flush()
	d.report()
}

func (d *dumper) dmrBurst(key string, b []byte) {
	var p [33]byte
	copy(p[:], b)
	s := d.get(key, ambe.DMR)
	for _, f := range ambe.ExtractDMR(p) {
		d.frame(s, f)
	}
}

func (d *dumper) udp(p udpPacket) {
	b := p.payload
	switch {
	case len(b) >= 53 && string(b[:4]) == "DMRD":
		// HomeBrew: seq(4) src(5..7) dst(8..10) rptr(11..14) bits(15) stream(16..19) data(20..52)
		if b[15]&0x20 != 0 { // data sync: not voice
			return
		}
		d.dmrBurst(fmt.Sprintf("dmr-%08x", binary.BigEndian.Uint32(b[16:20])), b[20:53])
	default:
		off := -1
		if len(b) >= 27 && string(b[:4]) == "DSVT" {
			off = 0
		} else if len(b) >= 29 && string(b[2:6]) == "DSVT" { // DPlus length prefix
			off = 2
		}
		if off < 0 || b[off+4] != 0x20 || len(b) < off+27 {
			return
		}
		var f [9]byte
		copy(f[:], b[off+15:off+24])
		s := d.get(fmt.Sprintf("dstar-%04x", binary.BigEndian.Uint16(b[off+12:off+14])), ambe.DStar)
		d.frame(s, f)
	}
}

func (d *dumper) raw(r io.Reader, m ambe.Mode) error {
	br := bufio.NewReader(r)
	if m == ambe.DMR {
		var p [33]byte
		for {
			if _, err := io.ReadFull(br, p[:]); err != nil {
				return eofOK(err)
			}
			d.dmrBurst("raw", p[:])
		}
	}
	s := d.get("raw", m)
	var f [9]byte
	for {
		if _, err := io.ReadFull(br, f[:]); err != nil {
			return eofOK(err)
		}
		d.frame(s, f)
	}
}

func (d *dumper) hexLines(r io.Reader, m ambe.Mode) error {
	sc := bufio.NewScanner(r)
	for ln := 1; sc.Scan(); ln++ {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		t := strings.Join(strings.Fields(line), "")
		if t == "" {
			continue
		}
		b, err := hex.DecodeString(t)
		if err != nil {
			return fmt.Errorf("line %d: %v", ln, err)
		}
		switch {
		case m == ambe.DMR && len(b) == 33:
			d.dmrBurst("hex", b)
		case len(b) == 9 || (m == ambe.DStar && len(b) == 12):
			var f [9]byte
			copy(f[:], b)
			d.frame(d.get("hex", m), f)
		default:
			return fmt.Errorf("line %d: %d bytes unexpected for %v", ln, len(b), m)
		}
	}
	return sc.Err()
}

func (d *dumper) report() {
	sort.Strings(d.order)
	e := os.Stderr
	fmt.Fprintf(e, "%-16s %-5s %7s %6s %6s %6s %6s %8s %8s %8s %8s\n",
		"stream", "mode", "frames", "voice", "sil", "tone", "eras", "fec_bad%", "octave%", "f0jump%", "vuvflip%")
	for _, k := range d.order {
		s := d.streams[k]
		pct := func(a, b int) float64 {
			if b == 0 {
				return 0
			}
			return 100 * float64(a) / float64(b)
		}
		v := s.kinds[mbe.Voice]
		fmt.Fprintf(e, "%-16s %-5s %7d %6d %6d %6d %6d %8.2f %8.2f %8.2f %8.2f\n",
			k, s.mode, s.frames, v, s.kinds[mbe.Silence], s.kinds[mbe.Tone], s.kinds[mbe.Erasure],
			pct(s.fecBad, s.frames), pct(s.octave, v), pct(s.bigStep, v), pct(s.vuvFlip, s.vuvTot))
	}
}

func eofOK(err error) error {
	if err == io.EOF {
		return nil
	}
	if err == io.ErrUnexpectedEOF {
		return fmt.Errorf("trailing partial frame")
	}
	return err
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "mbedump:", err)
	os.Exit(1)
}
