package bridge

import (
	"testing"
	"time"

	"github.com/2E0LXY/dvxcode/dstar"
	"github.com/2E0LXY/dvxcode/hbp"
)

// Hostile network input must never panic the event loop.
func FuzzEvents(f *testing.F) {
	f.Add([]byte("DMRD"), []byte("DSVT"))
	f.Fuzz(func(t *testing.T, a, b []byte) {
		dm := &fakeDMR{in: make(chan hbp.Data, 1)}
		ds := &fakeDS{in: make(chan dstar.Packet, 1)}
		br := New(cfg(), dm, ds, testDB(), quiet)
		now := time.Unix(1000, 0)
		br.now = func() time.Time { return now }
		pk := append(make([]byte, 0, 64), a...)
		for len(pk) < 64 {
			pk = append(pk, byte(len(pk)))
		}
		if d, ok := hbp.Unmarshal(append([]byte("DMRD"), pk...)); ok {
			d.Slot, d.Dst = 2, 235
			br.onDMR(d)
			d.FrameType, d.DTypeVSeq = hbp.FrameDataSync, 2
			br.onDMR(d)
		}
		if p, ok := dstar.ParseDSVT(b); ok {
			br.onDStar(p)
		}
		var amb [9]byte
		copy(amb[:], a)
		br.onDStar(dstar.Packet{StreamID: 9, Seq: uint8(len(b) % 21), AMBE: amb})
		for i := 0; i < 50; i++ {
			now = now.Add(20 * time.Millisecond)
			br.tick()
		}
	})
}
