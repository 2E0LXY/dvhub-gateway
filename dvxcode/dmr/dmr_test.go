package dmr

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// Golden vectors from MMDVMHost (reference/dmr_ref.cpp).
func TestGoldenMMDVMHost(t *testing.T) {
	f, err := os.Open("../testdata/dmr_golden.txt")
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<16), 1<<16)
	var nLC, nEMB, nST int
	for s.Scan() {
		fs := strings.Fields(s.Text())
		switch fs[0] {
		case "LC":
			var l LC
			copy(l[:], mustHex(fs[1]))
			for _, k := range []struct {
				dt  uint8
				ref string
			}{{DTVoiceHeader, fs[3]}, {DTTerminator, fs[5]}} {
				var b Burst
				EncodeFullLC(l, k.dt, 0, &b)
				want := mustHex(k.ref)
				for i := 0; i < 264; i++ {
					if (i >= 98 && i < 166) || bit(b[:], i) == bit(want, i) {
						continue
					}
					t.Fatalf("full LC dt=%d bit %d differs for %x", k.dt, i, l)
				}
				var w Burst
				copy(w[:], want)
				if got, ok := DecodeFullLC(&w, k.dt); !ok || got != l {
					t.Fatalf("decode of MMDVMHost full LC failed")
				}
			}
			fr := EmbeddedFragments(l)
			for n := 0; n < 4; n++ {
				p := strings.SplitN(fs[7+n], ":", 2)
				want := mustHex(p[1]) // bytes 13..19
				for i := 0; i < 32; i++ {
					if bit(fr[n][:], i) != bit(want, 116-104+i) {
						t.Fatalf("embedded fragment %d bit %d differs", n, i)
					}
				}
			}
			if got, ok := DecodeEmbedded(fr); !ok || got != l {
				t.Fatal("embedded round trip")
			}
			nLC++
		case "EMB":
			var cc, pi, lcss int
			fmt.Sscan(fs[1], &cc)
			fmt.Sscan(fs[2], &pi)
			fmt.Sscan(fs[3], &lcss)
			var b Burst
			SetEMB(&b, uint8(cc), pi == 1, uint8(lcss), [4]byte{})
			if got := b[13:20]; hex.EncodeToString(got) != fs[4] {
				t.Fatalf("EMB %d %d %d: %x want %s", cc, pi, lcss, got, fs[4])
			}
			c2, p2, l2, _, e := EMB(&b)
			if int(c2) != cc || p2 != (pi == 1) || int(l2) != lcss || e != 0 {
				t.Fatal("EMB decode")
			}
			nEMB++
		case "SLOT":
			var cc, dt int
			fmt.Sscan(fs[1], &cc)
			fmt.Sscan(fs[2], &dt)
			var b Burst
			SetSlotType(&b, uint8(cc), uint8(dt))
			if got := b[12:21]; hex.EncodeToString(got) != fs[3] {
				t.Fatalf("slot type %d %d: %x want %s", cc, dt, got, fs[3])
			}
			nST++
		}
	}
	if nLC == 0 || nEMB == 0 || nST == 0 {
		t.Fatal("no golden vectors")
	}
	t.Logf("matched %d LCs (header, terminator, embedded), %d EMB, %d slot types", nLC, nEMB, nST)
}

func TestBPTCCorrectsErrors(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for n := 0; n < 2000; n++ {
		var l LC
		r.Read(l[:])
		var b Burst
		EncodeFullLC(l, DTVoiceHeader, 1, &b)
		for k := 0; k < 3; k++ { // scattered single errors
			i := r.Intn(196)
			p := rawPos(i)
			b[p>>3] ^= 0x80 >> (p & 7)
		}
		got, ok := DecodeFullLC(&b, DTVoiceHeader)
		if ok && got != l {
			t.Fatal("miscorrection accepted")
		}
	}
}

func TestAMBEPlacement(t *testing.T) {
	var f [3][9]byte
	for i := range f {
		for j := range f[i] {
			f[i][j] = byte(i*9 + j + 1)
		}
	}
	var b Burst
	SetSync(&b, SyncBSVoice)
	SetAMBE(&b, f)
	if AMBE(&b) != f || SyncDistance(&b, SyncBSVoice) != 0 {
		t.Fatal("AMBE/sync placement")
	}
}
