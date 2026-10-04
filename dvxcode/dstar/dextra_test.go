package dstar

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func TestDExtraLinkAndStream(t *testing.T) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	got := make(chan []byte, 8)
	var peer *net.UDPAddr
	linked := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, a, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			peer = a
			p := append([]byte(nil), buf[:n]...)
			switch {
			case n == 11 && p[9] != ' ': // link request: reply as xlxd (rev 0)
				select {
				case linked <- p:
				default:
				}
				pc.WriteToUDP(append(p[:10], 'A', 'C', 'K', 0), a)
			case n >= 27:
				got <- p
			}
		}
	}()
	x := NewDExtra(DExtraConfig{Reflector: pc.LocalAddr().String(), Callsign: "2E0LXY", LocalModule: 'B', ReflectorModule: 'C'},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go x.Run(ctx)
	req := <-linked
	if string(req[:8]) != "2E0LXY  " || req[8] != 'B' || req[9] != 'C' {
		t.Fatalf("link request %q", req)
	}
	for !x.Up() {
		time.Sleep(10 * time.Millisecond)
	}
	h := Header{MY: "M0ABC", YOUR: "CQCQCQ", RPT1: "2E0LXY B", RPT2: "2E0LXY G", Suffix: "DMR"}
	x.SendHeader(0x4242, h)
	x.SendVoice(0x4242, 5, false, [9]byte{1, 2, 3}, [3]byte{4, 5, 6})
	x.SendVoice(0x4242, 6, true, [9]byte{}, [3]byte{})
	hp, _ := ParseDSVT(<-got)
	vp, _ := ParseDSVT(<-got)
	lp, _ := ParseDSVT(<-got)
	if hp.Header == nil || hp.Header.Callsign() != "M0ABC" || hp.StreamID != 0x4242 {
		t.Fatalf("header %+v", hp)
	}
	if vp.Seq != 5 || vp.AMBE[2] != 3 || vp.Data[0] != 4 || lp.Seq != 6 || !lp.Last || lp.AMBE != EndAMBE {
		t.Fatalf("voice %+v last %+v", vp, lp)
	}
	pc.WriteToUDP(MarshalVoice(0x7777, 3, false, [9]byte{9}, [3]byte{}), peer)
	select {
	case p := <-x.Recv():
		if p.StreamID != 0x7777 || p.Seq != 3 || p.AMBE[0] != 9 {
			t.Fatalf("rx %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no rx")
	}
}
