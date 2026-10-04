package bridge

import (
	"bufio"
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2E0LXY/dvxcode/ambe"
	"github.com/2E0LXY/dvxcode/dmr"
	"github.com/2E0LXY/dvxcode/dstar"
	"github.com/2E0LXY/dvxcode/hbp"
	"github.com/2E0LXY/dvxcode/ids"
)

type fakeDMR struct {
	in  chan hbp.Data
	mu  sync.Mutex
	out []hbp.Data
}

func (f *fakeDMR) Send(d hbp.Data) error {
	f.mu.Lock()
	f.out = append(f.out, d)
	f.mu.Unlock()
	return nil
}
func (f *fakeDMR) Recv() <-chan hbp.Data { return f.in }
func (f *fakeDMR) sent() []hbp.Data {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]hbp.Data(nil), f.out...)
}

type dsSent struct {
	id   uint16
	hdr  *dstar.Header
	seq  uint8
	last bool
	ambe [9]byte
	data [3]byte
}

type fakeDS struct {
	in  chan dstar.Packet
	mu  sync.Mutex
	out []dsSent
}

func (f *fakeDS) SendHeader(id uint16, h dstar.Header) error {
	f.mu.Lock()
	f.out = append(f.out, dsSent{id: id, hdr: &h})
	f.mu.Unlock()
	return nil
}
func (f *fakeDS) SendVoice(id uint16, seq uint8, last bool, a [9]byte, d [3]byte) error {
	f.mu.Lock()
	f.out = append(f.out, dsSent{id: id, seq: seq, last: last, ambe: a, data: d})
	f.mu.Unlock()
	return nil
}
func (f *fakeDS) Recv() <-chan dstar.Packet { return f.in }
func (f *fakeDS) sent() []dsSent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dsSent(nil), f.out...)
}

func vectors(t *testing.T) [][9]byte {
	fh, err := os.Open("../testdata/vectors/dstar_real.hex")
	if err != nil {
		t.Skip(err)
	}
	defer fh.Close()
	var out [][9]byte
	s := bufio.NewScanner(fh)
	for s.Scan() {
		l := s.Text()
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		b, _ := hex.DecodeString(l)
		var f [9]byte
		copy(f[:], b)
		out = append(out, f)
	}
	return out
}

func testDB() *ids.DB {
	db := ids.New(nil)
	db.Load(strings.NewReader("2341234\tM0ABC\tAlice\n2351999\t2E0LXY\tDaren\n"))
	return db
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func cfg() Config {
	return Config{Slot: 2, Talkgroup: 235, ColourCode: 1, FallbackID: 2351999, GatewayCall: "2E0LXY",
		Module: 'B', Tick: time.Millisecond, Timeout: 2 * time.Second, Lookahead: true, MaxQueue: 100000}
}

func waitFor(t *testing.T, cond func() bool) {
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var wakefield = dstar.Position{Lat: 53.6833, Lon: -1.4977}

// dstarToDMR runs a real D-STAR stream through a bridge and returns what the
// DMR side received.
func dstarToDMR(t *testing.T, n int) []hbp.Data {
	vec := vectors(t)[:n]
	dm := &fakeDMR{in: make(chan hbp.Data, 1024)}
	ds := &fakeDS{in: make(chan dstar.Packet, 1024)}
	b := New(cfg(), dm, ds, testDB(), quiet)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	h := dstar.Header{RPT1: "XLX235 B", RPT2: "XLX235 G", YOUR: "CQCQCQ", MY: "M0ABC", Suffix: "ID51"}
	mux := dstar.NewMuxer("Hello DMR world", &h, dstar.DPRS("M0ABC", wakefield, "test"))
	ds.in <- dstar.Packet{StreamID: 0x1234, Header: &h}
	for i, f := range vec {
		seq := uint8(i % 21)
		ds.in <- dstar.Packet{StreamID: 0x1234, Seq: seq, AMBE: f, Data: mux.Next(int(seq))}
	}
	ds.in <- dstar.Packet{StreamID: 0x1234, Seq: uint8(len(vec) % 21), Last: true}
	waitFor(t, func() bool {
		s := dm.sent()
		return len(s) > 0 && s[len(s)-1].FrameType == hbp.FrameDataSync && s[len(s)-1].DTypeVSeq == dmr.DTTerminator
	})
	return dm.sent()
}

func TestDStarToDMRMetadataAndVoice(t *testing.T) {
	const n = 300
	out := dstarToDMR(t, n)
	hdr := dmr.Burst(out[0].Burst)
	lc, ok := dmr.DecodeFullLC(&hdr, dmr.DTVoiceHeader)
	if !ok || lc.Src() != 2341234 || lc.Dst() != 235 || out[0].Src != 2341234 || out[0].Slot != 2 {
		t.Fatalf("header LC %x ok=%v pkt src %d", lc, ok, out[0].Src)
	}
	var ta dmr.TalkerAlias
	alias, gotGPS, voiceLC := "", false, false
	frames := 0
	var frags [][4]byte
	for i, d := range out[1 : len(out)-1] {
		bu := dmr.Burst(d.Burst)
		if want := i % 6; (want == 0) != (d.FrameType == hbp.FrameVoiceSync) || (want > 0 && int(d.DTypeVSeq) != want) {
			t.Fatalf("burst %d: frame type %d vseq %d", i, d.FrameType, d.DTypeVSeq)
		}
		if d.FrameType == hbp.FrameVoiceSync {
			if dmr.SyncDistance(&bu, dmr.SyncBSVoice) != 0 {
				t.Fatal("voice sync")
			}
		} else {
			cc, _, lcss, frag, e := dmr.EMB(&bu)
			if cc != 1 || e != 0 {
				t.Fatal("EMB")
			}
			switch lcss {
			case 1:
				frags = [][4]byte{frag}
			case 3:
				frags = append(frags, frag)
			case 2:
				if len(frags) == 3 {
					l, ok := dmr.DecodeEmbedded([4][4]byte{frags[0], frags[1], frags[2], frag})
					if !ok {
						t.Fatal("embedded LC invalid")
					}
					switch l.FLCO() {
					case dmr.FLCOGroup:
						voiceLC = l.Src() == 2341234 && l.Dst() == 235
					case dmr.FLCOGPSInfo:
						la, lo, _, _ := dmr.GPS(l)
						gotGPS = math.Abs(la-wakefield.Lat) < 1e-3 && math.Abs(lo-wakefield.Lon) < 1e-3
					default:
						if s, ok := ta.Add(l); ok {
							alias = s
						}
					}
				}
			}
		}
		for _, f := range dmr.AMBE(&bu) {
			if _, st := ambe.Decode(ambe.DMR, f); st.Total() != 0 || !st.C0Parity {
				t.Fatal("AMBE FEC errors in output")
			}
			frames++
		}
	}
	term := dmr.Burst(out[len(out)-1].Burst)
	if l, ok := dmr.DecodeFullLC(&term, dmr.DTTerminator); !ok || l.Src() != 2341234 {
		t.Fatal("terminator")
	}
	if !voiceLC || alias != "M0ABC /ID51 Hello DMR world" || !gotGPS {
		t.Fatalf("embedded metadata: voiceLC=%v alias=%q gps=%v", voiceLC, alias, gotGPS)
	}
	if frames < n || frames > n+3 {
		t.Fatalf("frames %d for %d input", frames, n)
	}
	t.Logf("%d DMR packets, alias %q, GPS ok, %d AMBE frames", len(out), alias, frames)
}

func TestDMRToDStarMetadataAndVoice(t *testing.T) {
	in := dstarToDMR(t, 300) // a realistic DMR call with TA + GPS
	dm := &fakeDMR{in: make(chan hbp.Data, 1024)}
	ds := &fakeDS{in: make(chan dstar.Packet, 1024)}
	b := New(cfg(), dm, ds, testDB(), quiet)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	for _, d := range in {
		d.StreamID ^= 0x5555 // not one of this bridge's own streams
		dm.in <- d
	}
	waitFor(t, func() bool {
		s := ds.sent()
		return len(s) > 0 && s[len(s)-1].last
	})
	out := ds.sent()
	if out[0].hdr == nil || out[0].hdr.Callsign() != "M0ABC" || strings.TrimSpace(out[0].hdr.Suffix) != "DMR" {
		t.Fatalf("header %+v", out[0].hdr)
	}
	var d dstar.Demuxer
	frames := 0
	for i, o := range out[1 : len(out)-1] {
		if int(o.seq) != i%21 {
			t.Fatalf("seq %d at %d", o.seq, i)
		}
		if _, st := ambe.Decode(ambe.DStar, o.ambe); st.Total() != 0 || !st.C1Parity {
			t.Fatal("D-STAR FEC errors in output")
		}
		d.Add(int(o.seq), o.data)
		frames++
	}
	if d.Header == nil || d.Header.Callsign() != "M0ABC" {
		t.Fatal("slow-data header")
	}
	if d.Text != "M0ABC /ID51 Hello DM" { // D-STAR text messages are 20 chars
		t.Fatalf("text %q", d.Text)
	}
	if d.Pos == nil || math.Abs(d.Pos.Lat-wakefield.Lat) > 1e-3 || math.Abs(d.Pos.Lon-wakefield.Lon) > 1e-3 {
		t.Fatalf("position %+v", d.Pos)
	}
	t.Logf("D-STAR: header %s, text %q, pos %.4f,%.4f, %d frames", out[0].hdr.MY, d.Text, d.Pos.Lat, d.Pos.Lon, frames)
}

// A stalled network sink must not grow latency without bound, and dropping
// must keep the DMR superframe sequence intact.
func TestQueueCapKeepsSuperframes(t *testing.T) {
	vec := vectors(t)[:300]
	dm := &fakeDMR{in: make(chan hbp.Data, 1)}
	ds := &fakeDS{in: make(chan dstar.Packet, 1)}
	c := cfg()
	c.MaxQueue = 60 // 20 bursts
	b := New(c, dm, ds, testDB(), quiet)
	now := time.Unix(1000, 0)
	b.now = func() time.Time { return now }
	h := dstar.Header{MY: "M0ABC", YOUR: "CQCQCQ"}
	b.onDStar(dstar.Packet{StreamID: 5, Header: &h})
	for i, f := range vec {
		b.onDStar(dstar.Packet{StreamID: 5, Seq: uint8(i % 21), AMBE: f})
	}
	b.tick() // queue-cap check runs on tick
	if n := len(b.toDMR.out); n > 20+6+1 {
		t.Fatalf("queue %d bursts after cap", n)
	}
	if b.Snapshot().Dropped == 0 {
		t.Fatal("no drops counted")
	}
	out := b.toDMR.out
	if out[0].FrameType != hbp.FrameDataSync {
		t.Fatal("header dropped")
	}
	for i, d := range out[1:] {
		want := i % 6
		if (want == 0) != (d.FrameType == hbp.FrameVoiceSync) {
			t.Fatalf("superframe broken at %d", i)
		}
	}
}

func TestTimeOutTimer(t *testing.T) {
	dm := &fakeDMR{in: make(chan hbp.Data, 1)}
	ds := &fakeDS{in: make(chan dstar.Packet, 1)}
	c := cfg()
	c.MaxCall = time.Second
	b := New(c, dm, ds, testDB(), quiet)
	now := time.Unix(1000, 0)
	b.now = func() time.Time { return now }
	vec := vectors(t)
	h := dstar.Header{MY: "M0ABC", YOUR: "CQCQCQ"}
	b.onDStar(dstar.Packet{StreamID: 6, Header: &h})
	for i := 0; i < 100; i++ {
		now = now.Add(20 * time.Millisecond)
		b.onDStar(dstar.Packet{StreamID: 6, Seq: uint8(i % 21), AMBE: vec[i%len(vec)]})
		b.tick()
	}
	if b.Snapshot().TOT != 1 {
		t.Fatalf("TOT not triggered: %+v", b.Snapshot())
	}
}

func TestPanicRecovery(t *testing.T) {
	dm := &fakeDMR{in: make(chan hbp.Data, 1)}
	ds := &fakeDS{in: make(chan dstar.Packet, 1)}
	b := New(cfg(), dm, ds, testDB(), quiet)
	b.safe("test", func() { panic("boom") })
	if b.Snapshot().Panics != 1 {
		t.Fatal("panic not recovered/counted")
	}
}

func TestUnknownCallsignUsesFallback(t *testing.T) {
	dm := &fakeDMR{in: make(chan hbp.Data, 64)}
	ds := &fakeDS{in: make(chan dstar.Packet, 64)}
	b := New(cfg(), dm, ds, testDB(), quiet)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	h := dstar.Header{MY: "G9ZZZ", YOUR: "CQCQCQ"}
	ds.in <- dstar.Packet{StreamID: 7, Header: &h}
	ds.in <- dstar.Packet{StreamID: 7, Seq: 1, Last: true}
	waitFor(t, func() bool { return len(dm.sent()) >= 2 })
	if dm.sent()[0].Src != 2351999 {
		t.Fatalf("src %d", dm.sent()[0].Src)
	}
}
