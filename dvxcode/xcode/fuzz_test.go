package xcode

import (
	"testing"

	"github.com/2E0LXY/dvxcode/ambe"
)

// Arbitrary input frames must never panic and must always yield a clean
// (FEC-valid) target frame.
func FuzzTranscode(f *testing.F) {
	f.Add(dstarNull[:], dmrNull[:])
	f.Fuzz(func(t *testing.T, a, b []byte) {
		for _, dir := range [][2]ambe.Mode{{ambe.DStar, ambe.DMR}, {ambe.DMR, ambe.DStar}} {
			tc := New(dir[0], dir[1], DefaultOptions)
			for i := 0; i+9 <= len(a) || i == 0; i += 9 {
				var fr [9]byte
				copy(fr[:], a[min(i, len(a)):])
				out := tc.Frame(fr)
				if _, st := ambe.Decode(dir[1], out); st.Total() != 0 || !st.C0Parity || !st.C1Parity {
					t.Fatalf("dirty output frame %x", out)
				}
				if i == 0 && len(a) < 9 {
					break
				}
			}
			tc.Lost()
			var fr [9]byte
			copy(fr[:], b)
			tc.Frame(fr)
			tc.Flush()
		}
	})
}
