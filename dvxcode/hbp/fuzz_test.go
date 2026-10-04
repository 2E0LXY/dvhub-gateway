package hbp

import "testing"

func FuzzUnmarshal(f *testing.F) {
	d := Data{Src: 1, Dst: 2, Slot: 2, StreamID: 3}
	f.Add(d.Marshal())
	f.Fuzz(func(t *testing.T, b []byte) {
		if d, ok := Unmarshal(b); ok {
			r, ok2 := Unmarshal(d.Marshal())
			if !ok2 || r.Src != d.Src || r.Dst != d.Dst || r.StreamID != d.StreamID || r.Burst != d.Burst {
				t.Fatal("round trip")
			}
		}
	})
}
