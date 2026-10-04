// dvxcode transcodes a stream of 9-byte AMBE frames (network byte order)
// from stdin to stdout. One process per voice stream; restart per call.
//
//	dvxcode -from dstar -to dmr < in.ambe > out.ambe
//	dvxcode -from dmr -to dstar -hex < frames.hex
package main

import (
	"bufio"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/2E0LXY/dvxcode/ambe"
	"github.com/2E0LXY/dvxcode/xcode"
)

func mode(s string) ambe.Mode {
	switch s {
	case "dstar":
		return ambe.DStar
	case "dmr", "ysf", "nxdn", "p25p2":
		return ambe.DMR // AMBE+2 3600x2450 family
	}
	fmt.Fprintf(os.Stderr, "dvxcode: unknown mode %q\n", s)
	os.Exit(2)
	return 0
}

func main() {
	from := flag.String("from", "dstar", "source: dstar | dmr | ysf | nxdn | p25p2")
	to := flag.String("to", "dmr", "target: dstar | dmr | ysf | nxdn | p25p2")
	hexIO := flag.Bool("hex", false, "hex lines instead of raw bytes")
	noLook := flag.Bool("low-latency", false, "disable 20 ms lookahead (less pitch repair)")
	stats := flag.Bool("stats", false, "print statistics to stderr at end")
	flag.Parse()

	opt := xcode.DefaultOptions
	opt.Lookahead = !*noLook
	t := xcode.New(mode(*from), mode(*to), opt)
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	put := func(f [9]byte) {
		if *hexIO {
			fmt.Fprintf(w, "%x\n", f)
		} else {
			w.Write(f[:])
		}
	}
	if *hexIO {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			l := sc.Text()
			if i := strings.IndexByte(l, '#'); i >= 0 {
				l = l[:i]
			}
			b, err := hex.DecodeString(strings.TrimSpace(l))
			if err != nil || len(b) != 9 {
				continue
			}
			var f [9]byte
			copy(f[:], b)
			put(t.Frame(f))
		}
	} else {
		r := bufio.NewReader(os.Stdin)
		var f [9]byte
		for {
			if _, err := io.ReadFull(r, f[:]); err != nil {
				break
			}
			put(t.Frame(f))
		}
	}
	for _, f := range t.Flush() {
		put(f)
	}
	if *stats {
		w.Flush()
		fmt.Fprintf(os.Stderr, "%+v\n", t.Stats)
	}
}
