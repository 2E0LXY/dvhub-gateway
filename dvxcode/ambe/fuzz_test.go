package ambe

import "testing"

func FuzzDecode(f *testing.F) {
	f.Add(dstarSilence[:])
	f.Add(dmrSilence[:])
	f.Fuzz(func(t *testing.T, b []byte) {
		var fr [9]byte
		copy(fr[:], b)
		for _, m := range []Mode{DStar, DMR} {
			d, _ := Decode(m, fr)
			if m == DStar {
				d[24] = 0
			}
			// Re-encoding the corrected bits must decode to the same bits, cleanly.
			d2, st := Decode(m, Encode(m, d))
			if d2 != d || st.Total() != 0 {
				t.Fatalf("%v: re-encode mismatch", m)
			}
		}
		var p [33]byte
		copy(p[:], b)
		_ = ExtractDMR(p)
	})
}
