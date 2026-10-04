package hbp

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// fakeMaster implements the master side of the login handshake.
func fakeMaster(t *testing.T, password string, got chan<- []byte) (*net.UDPConn, func(b []byte)) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	var peer *net.UDPAddr
	salt := []byte{1, 2, 3, 4}
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
			case string(p[:4]) == "RPTL":
				pc.WriteToUDP(append([]byte("RPTACK"), salt...), a)
			case string(p[:4]) == "RPTK":
				h := sha256.Sum256(append(append([]byte(nil), salt...), password...))
				if string(p[8:40]) != string(h[:]) {
					pc.WriteToUDP([]byte("MSTNAK"), a)
					continue
				}
				pc.WriteToUDP([]byte("RPTACK"), a)
			case string(p[:4]) == "RPTC":
				if n != 302 {
					t.Errorf("RPTC length %d", n)
				}
				pc.WriteToUDP([]byte("RPTACK"), a)
			case string(p[:7]) == "RPTPING":
				pc.WriteToUDP([]byte("MSTPONG"), a)
			case string(p[:4]) == "DMRD":
				got <- p
			}
		}
	}()
	return pc, func(b []byte) { pc.WriteToUDP(b, peer) }
}

func TestLoginAndTraffic(t *testing.T) {
	got := make(chan []byte, 4)
	pc, inject := fakeMaster(t, "passw0rd", got)
	defer pc.Close()
	c := New(Config{Master: pc.LocalAddr().String(), ID: 235199901, Password: "passw0rd", Callsign: "2E0LXY",
		ColourCode: 1, Slots: 3}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for !c.Up() {
		if time.Now().After(deadline) {
			t.Fatal("login did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	d := Data{Seq: 7, Src: 2341234, Dst: 235, Slot: 2, FrameType: FrameVoiceSync, StreamID: 0xdeadbeef}
	if err := c.Send(d); err != nil {
		t.Fatal(err)
	}
	p := <-got
	r, ok := Unmarshal(p)
	if !ok || r.Src != 2341234 || r.Dst != 235 || r.Slot != 2 || r.FrameType != FrameVoiceSync || r.StreamID != 0xdeadbeef ||
		binary.BigEndian.Uint32(p[11:]) != 235199901 {
		t.Fatalf("sent %+v", r)
	}
	r.Src = 999
	inject(r.Marshal())
	select {
	case in := <-c.Recv():
		if in.Src != 999 {
			t.Fatal("rx")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no rx")
	}
}

func TestWrongPasswordRejected(t *testing.T) {
	got := make(chan []byte, 1)
	pc, _ := fakeMaster(t, "right", got)
	defer pc.Close()
	c := New(Config{Master: pc.LocalAddr().String(), ID: 1, Password: "wrong"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	go c.Run(ctx)
	<-ctx.Done()
	if c.Up() {
		t.Fatal("logged in with wrong password")
	}
}
