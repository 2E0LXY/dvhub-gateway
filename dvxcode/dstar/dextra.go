package dstar

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"
)

// Packet is a received DExtra stream element.
type Packet struct {
	StreamID uint16
	Header   *Header // non-nil for a header packet
	Seq      uint8   // 0..20
	Last     bool
	AMBE     [9]byte
	Data     [3]byte
}

// DExtraConfig links a gateway callsign/module to a reflector module.
type DExtraConfig struct {
	Reflector       string // host:port (usually :30001)
	Callsign        string // our gateway callsign (≤7 chars)
	LocalModule     byte   // e.g. 'B'
	ReflectorModule byte   // e.g. 'C'
}

// DExtra is a reflector link client.
type DExtra struct {
	cfg  DExtraConfig
	log  *slog.Logger
	rx   chan Packet
	mu   sync.Mutex
	conn *net.UDPConn
	up   bool
}

// NewDExtra creates a client; call Run.
func NewDExtra(cfg DExtraConfig, log *slog.Logger) *DExtra {
	return &DExtra{cfg: cfg, log: log, rx: make(chan Packet, 256)}
}

// Recv delivers stream packets from the reflector.
func (x *DExtra) Recv() <-chan Packet { return x.rx }

// Up reports whether the link is acknowledged.
func (x *DExtra) Up() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.up
}

func (x *DExtra) call8() []byte { return pad(x.cfg.Callsign, 8) }

var dsvtHeaderTag = []byte{'D', 'S', 'V', 'T', 0x10, 0, 0, 0, 0x20, 0, 1, 2}
var dsvtVoiceTag = []byte{'D', 'S', 'V', 'T', 0x20, 0, 0, 0, 0x20, 0, 1, 2}

// MarshalHeader builds a 56-byte DSVT header packet.
func MarshalHeader(id uint16, h Header) []byte {
	b := append([]byte(nil), dsvtHeaderTag...)
	b = binary.LittleEndian.AppendUint16(b, id)
	b = append(b, 0x80)
	hb := h.Bytes()
	return append(b, hb[:]...)
}

// MarshalVoice builds a 27-byte DSVT voice packet.
func MarshalVoice(id uint16, seq uint8, last bool, ambe [9]byte, data [3]byte) []byte {
	b := append([]byte(nil), dsvtVoiceTag...)
	b = binary.LittleEndian.AppendUint16(b, id)
	s := seq % 21
	if last {
		s |= 0x40
		ambe, data = EndAMBE, EndData
	}
	b = append(b, s)
	b = append(b, ambe[:]...)
	return append(b, data[:]...)
}

// ParseDSVT decodes a DSVT header or voice packet.
func ParseDSVT(b []byte) (Packet, bool) {
	var p Packet
	if len(b) < 27 || string(b[:4]) != "DSVT" || b[8] != 0x20 {
		return p, false
	}
	p.StreamID = binary.LittleEndian.Uint16(b[12:14])
	if b[4] == 0x10 && len(b) >= 56 {
		h, ok := ParseHeader(b[15:56])
		if !ok {
			// Many gateways send headers with zero CRC; accept if fields look sane.
			if strings.TrimSpace(h.MY) == "" {
				return p, false
			}
		}
		p.Header = &h
		return p, true
	}
	if b[4] != 0x20 {
		return p, false
	}
	p.Seq = b[14] & 0x1f
	p.Last = b[14]&0x40 != 0
	copy(p.AMBE[:], b[15:24])
	copy(p.Data[:], b[24:27])
	return p, true
}

func (x *DExtra) write(b []byte) error {
	x.mu.Lock()
	c, up := x.conn, x.up
	x.mu.Unlock()
	if c == nil || !up {
		return fmt.Errorf("dextra: not linked")
	}
	_, err := c.Write(b)
	return err
}

// SendHeader starts an outgoing stream.
func (x *DExtra) SendHeader(id uint16, h Header) error { return x.write(MarshalHeader(id, h)) }

// SendVoice sends one voice frame (last=true ends the stream).
func (x *DExtra) SendVoice(id uint16, seq uint8, last bool, ambe [9]byte, data [3]byte) error {
	return x.write(MarshalVoice(id, seq, last, ambe, data))
}

// Run links and services the reflector connection until ctx ends.
func (x *DExtra) Run(ctx context.Context) error {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := x.session(ctx)
		if ctx.Err() != nil {
			break
		}
		if time.Since(start) > time.Minute {
			backoff = 2 * time.Second // session was healthy: reset
		}
		wait := backoff + time.Duration(rand.Int63n(int64(backoff/2)+1))
		x.log.Warn("dextra: session ended, reconnecting", "err", err, "in", wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		backoff = min(backoff*2, time.Minute)
	}
	return ctx.Err()
}

func (x *DExtra) session(ctx context.Context) error {
	ra, err := net.ResolveUDPAddr("udp", x.cfg.Reflector)
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp", nil, ra)
	if err != nil {
		return err
	}
	x.mu.Lock()
	x.conn = conn
	x.mu.Unlock()
	link := append(x.call8(), x.cfg.LocalModule, x.cfg.ReflectorModule, 0)
	defer func() {
		if x.Up() {
			conn.Write(append(x.call8(), x.cfg.LocalModule, ' ', 0)) // unlink
		}
		x.mu.Lock()
		x.up = false
		x.mu.Unlock()
		conn.Close()
	}()
	conn.Write(link)
	go func() { <-ctx.Done(); conn.SetReadDeadline(time.Now()) }()
	seen, lastKA, lastLink := time.Now(), time.Time{}, time.Now()
	buf := make([]byte, 2048)
	for ctx.Err() == nil {
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _ := conn.Read(buf)
		now := time.Now()
		if n > 0 {
			p := buf[:n]
			switch {
			case n == 14 && string(p[10:13]) == "ACK":
				x.mu.Lock()
				if !x.up {
					x.log.Info("dextra: linked", "reflector", x.cfg.Reflector, "module", string(x.cfg.ReflectorModule))
				}
				x.up = true
				x.mu.Unlock()
				seen = now
			case n == 14 && string(p[10:13]) == "NAK":
				return fmt.Errorf("link refused")
			case n == 11 && p[9] == x.cfg.LocalModule && p[8] == x.cfg.ReflectorModule: // XRF-style ack (modules swapped)
				x.mu.Lock()
				if !x.up {
					x.log.Info("dextra: linked", "reflector", x.cfg.Reflector, "module", string(x.cfg.ReflectorModule))
				}
				x.up = true
				x.mu.Unlock()
				seen = now
			case n == 9: // keepalive from reflector
				seen = now
			case n >= 27 && string(p[:4]) == "DSVT":
				seen = now
				if pk, ok := ParseDSVT(p); ok {
					select {
					case x.rx <- pk:
					default:
						x.log.Warn("dextra: rx queue full")
					}
				}
			}
		}
		if !x.Up() && now.Sub(lastLink) > 3*time.Second {
			conn.Write(link)
			lastLink = now
		}
		if x.Up() && now.Sub(lastKA) > 3*time.Second {
			conn.Write(append(x.call8(), x.cfg.LocalModule))
			lastKA = now
		}
		if now.Sub(seen) > 30*time.Second {
			return fmt.Errorf("reflector timeout")
		}
	}
	return ctx.Err()
}
