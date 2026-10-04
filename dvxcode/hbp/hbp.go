// Package hbp is a HomeBrew Protocol (MMDVM/BrandMeister/HBlink) peer
// client: login, authentication, configuration, keepalive and DMRD voice.
package hbp

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"sync"
	"time"
)

// FrameType values in DMRD byte 15 bits 5-4.
const (
	FrameVoice     = 0
	FrameVoiceSync = 1
	FrameDataSync  = 2
)

// Data is one DMRD packet.
type Data struct {
	Seq       uint8
	Src, Dst  uint32
	Repeater  uint32
	Slot      int // 1 or 2
	Private   bool
	FrameType int
	DTypeVSeq uint8 // data type (data sync) or voice sequence 0..5
	StreamID  uint32
	Burst     [33]byte
	BER, RSSI uint8
}

// Marshal encodes a DMRD packet.
func (d *Data) Marshal() []byte {
	b := make([]byte, 55)
	copy(b, "DMRD")
	b[4] = d.Seq
	b[5], b[6], b[7] = byte(d.Src>>16), byte(d.Src>>8), byte(d.Src)
	b[8], b[9], b[10] = byte(d.Dst>>16), byte(d.Dst>>8), byte(d.Dst)
	binary.BigEndian.PutUint32(b[11:], d.Repeater)
	if d.Slot == 2 {
		b[15] |= 0x80
	}
	if d.Private {
		b[15] |= 0x40
	}
	b[15] |= byte(d.FrameType&3)<<4 | d.DTypeVSeq&0x0f
	binary.LittleEndian.PutUint32(b[16:], d.StreamID) // MMDVM memcpy, x86 order
	copy(b[20:53], d.Burst[:])
	b[53], b[54] = d.BER, d.RSSI
	return b
}

// Unmarshal parses a DMRD packet.
func Unmarshal(b []byte) (Data, bool) {
	var d Data
	if len(b) < 53 || string(b[:4]) != "DMRD" {
		return d, false
	}
	d.Seq = b[4]
	d.Src = uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7])
	d.Dst = uint32(b[8])<<16 | uint32(b[9])<<8 | uint32(b[10])
	d.Repeater = binary.BigEndian.Uint32(b[11:])
	d.Slot = 1
	if b[15]&0x80 != 0 {
		d.Slot = 2
	}
	d.Private = b[15]&0x40 != 0
	d.FrameType = int(b[15]>>4) & 3
	d.DTypeVSeq = b[15] & 0x0f
	d.StreamID = binary.LittleEndian.Uint32(b[16:])
	copy(d.Burst[:], b[20:53])
	if len(b) >= 55 {
		d.BER, d.RSSI = b[53], b[54]
	}
	return d, true
}

// Config describes this peer to the master (RPTC).
type Config struct {
	Master      string // host:port
	ID          uint32 // peer (repeater/hotspot) ID
	Password    string
	Callsign    string
	RXFreq      uint32 // Hz
	TXFreq      uint32
	Power       int
	ColourCode  int
	Lat, Lon    float64
	Height      int
	Location    string
	Description string
	URL         string
	Slots       int    // 1, 2 or 3 (both)
	Options     string // optional RPTO string (e.g. "TS2=235;")
	Software    string
	Package     string
}

// ConfigBytes returns the 302-byte RPTC packet.
func (c *Config) ConfigBytes() []byte {
	f := func(v string, n int) string { return fmt.Sprintf("%-*.*s", n, n, v) }
	s := f(c.Callsign, 8) +
		fmt.Sprintf("%09d%09d", c.RXFreq, c.TXFreq) +
		fmt.Sprintf("%02d%02d", min(c.Power, 99), c.ColourCode) +
		fmt.Sprintf("%+08.4f%+09.4f", c.Lat, c.Lon) +
		fmt.Sprintf("%03d", min(c.Height, 999)) +
		f(c.Location, 20) + f(c.Description, 19) +
		fmt.Sprintf("%d", c.Slots) +
		f(c.URL, 124) + f(c.Software, 40) + f(c.Package, 40)
	b := make([]byte, 8, 302)
	copy(b, "RPTC")
	binary.BigEndian.PutUint32(b[4:], c.ID)
	return append(b, s...)
}

// Client maintains a session with a master.
type Client struct {
	cfg  Config
	conn *net.UDPConn
	log  *slog.Logger
	rx   chan Data
	mu   sync.Mutex
	up   bool
	seen time.Time
}

// New creates a client; call Run to connect.
func New(cfg Config, log *slog.Logger) *Client {
	return &Client{cfg: cfg, log: log, rx: make(chan Data, 256)}
}

// Recv delivers DMRD packets from the master.
func (c *Client) Recv() <-chan Data { return c.rx }

// Up reports whether the session is logged in.
func (c *Client) Up() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.up
}

// Send transmits a DMRD packet (dropped if not logged in).
func (c *Client) Send(d Data) error {
	c.mu.Lock()
	up, conn := c.up, c.conn
	c.mu.Unlock()
	if !up || conn == nil {
		return fmt.Errorf("hbp: not logged in")
	}
	d.Repeater = c.cfg.ID
	_, err := conn.Write(d.Marshal())
	return err
}

func (c *Client) setUp(v bool) {
	c.mu.Lock()
	if c.up != v {
		if v {
			c.log.Info("hbp: logged in", "master", c.cfg.Master, "id", c.cfg.ID)
		} else {
			c.log.Warn("hbp: session down", "master", c.cfg.Master)
		}
	}
	c.up = v
	c.mu.Unlock()
}

// Run connects and services the session until ctx ends, reconnecting as needed.
func (c *Client) Run(ctx context.Context) error {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			break
		}
		if time.Since(start) > time.Minute {
			backoff = 2 * time.Second // session was healthy: reset
		}
		wait := backoff + time.Duration(rand.Int63n(int64(backoff/2)+1))
		c.log.Warn("hbp: session ended, reconnecting", "err", err, "in", wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		backoff = min(backoff*2, time.Minute)
	}
	return ctx.Err()
}

type state int

const (
	stLogin state = iota
	stAuth
	stConfig
	stOptions
	stRunning
)

func (c *Client) session(ctx context.Context) error {
	raddr, err := net.ResolveUDPAddr("udp", c.cfg.Master)
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		if c.Up() {
			b := make([]byte, 9)
			copy(b, "RPTCL")
			binary.BigEndian.PutUint32(b[5:], c.cfg.ID)
			conn.Write(b)
		}
		c.setUp(false)
		conn.Close()
	}()
	id := make([]byte, 4)
	binary.BigEndian.PutUint32(id, c.cfg.ID)

	st := stLogin
	send := func() {
		switch st {
		case stLogin:
			conn.Write(append([]byte("RPTL"), id...))
		case stConfig:
			conn.Write(c.cfg.ConfigBytes())
		case stOptions:
			conn.Write(append(append([]byte("RPTO"), id...), c.cfg.Options...))
		}
	}
	send()
	var salt []byte
	pingT := time.NewTicker(5 * time.Second)
	defer pingT.Stop()
	retry := time.Now()
	c.seen = time.Now()
	buf := make([]byte, 2048)
	go func() { <-ctx.Done(); conn.SetReadDeadline(time.Now()) }()
	for ctx.Err() == nil {
		conn.SetReadDeadline(time.Now().Add(time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
				if ctx.Err() == nil {
					time.Sleep(time.Second) // ICMP unreachable etc: back off
				}
			}
		}
		now := time.Now()
		if n > 0 {
			p := buf[:n]
			switch {
			case len(p) >= 4 && string(p[:4]) == "DMRD":
				if st == stRunning {
					if d, ok := Unmarshal(p); ok {
						select {
						case c.rx <- d:
						default:
							c.log.Warn("hbp: rx queue full, dropping")
						}
					}
				}
				c.seen = now
			case len(p) >= 6 && string(p[:6]) == "RPTACK":
				c.seen = now
				switch st {
				case stLogin:
					if len(p) < 10 {
						return fmt.Errorf("short RPTACK")
					}
					salt = append([]byte(nil), p[6:10]...)
					h := sha256.Sum256(append(append([]byte(nil), salt...), c.cfg.Password...))
					conn.Write(append(append([]byte("RPTK"), id...), h[:]...))
					st = stAuth
				case stAuth:
					st = stConfig
					send()
				case stConfig:
					if c.cfg.Options != "" {
						st = stOptions
						send()
					} else {
						st = stRunning
						c.setUp(true)
					}
				case stOptions:
					st = stRunning
					c.setUp(true)
				}
				retry = now
			case len(p) >= 6 && string(p[:6]) == "MSTNAK":
				return fmt.Errorf("master refused (state %d)", st)
			case len(p) >= 5 && string(p[:5]) == "MSTCL":
				return fmt.Errorf("master closed session")
			case len(p) >= 7 && string(p[:7]) == "MSTPONG":
				c.seen = now
			}
		}
		if st != stRunning && now.Sub(retry) > 10*time.Second {
			st = stLogin
			send()
			retry = now
		}
		select {
		case <-pingT.C:
			if st == stRunning {
				conn.Write(append([]byte("RPTPING"), id...))
			}
		default:
		}
		if now.Sub(c.seen) > 60*time.Second {
			return fmt.Errorf("master timeout")
		}
	}
	return ctx.Err()
}
