// Package bridge joins a DMR talkgroup (HomeBrew) and a D-STAR reflector
// module (DExtra): voice is transcoded in the MBE parameter domain and
// metadata is translated in both directions:
//
//	D-STAR MY callsign  ⇄ DMR source ID (DMR ID database) + talker alias
//	D-STAR text message ⇄ DMR talker alias
//	D-STAR DPRS/NMEA    ⇄ DMR GPS info LC
//
// The bridge is half-duplex: one call at a time, first come first served.
package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/2E0LXY/dvxcode/ambe"
	"github.com/2E0LXY/dvxcode/dmr"
	"github.com/2E0LXY/dvxcode/dstar"
	"github.com/2E0LXY/dvxcode/hbp"
	"github.com/2E0LXY/dvxcode/ids"
	"github.com/2E0LXY/dvxcode/xcode"
)

// DMRNet is the HomeBrew side (satisfied by *hbp.Client).
type DMRNet interface {
	Send(hbp.Data) error
	Recv() <-chan hbp.Data
}

// DStarNet is the reflector side (satisfied by *dstar.DExtra).
type DStarNet interface {
	SendHeader(id uint16, h dstar.Header) error
	SendVoice(id uint16, seq uint8, last bool, ambe [9]byte, data [3]byte) error
	Recv() <-chan dstar.Packet
}

// Config controls routing and identity.
type Config struct {
	Slot        int    // DMR timeslot 1/2
	Talkgroup   uint32 // DMR destination (group call)
	ColourCode  uint8
	FallbackID  uint32 // DMR source ID for D-STAR callsigns not in the database
	GatewayCall string // D-STAR gateway callsign (RPT1/RPT2, unknown DMR users)
	Module      byte   // local D-STAR module letter
	Suffix      string // D-STAR MY suffix for DMR-originated calls (default "DMR")
	Tick        time.Duration
	Timeout     time.Duration // end a call after this much silence
	Lookahead   bool
}

func (c *Config) defaults() {
	if c.Tick == 0 {
		c.Tick = 20 * time.Millisecond
	}
	if c.Timeout == 0 {
		c.Timeout = 600 * time.Millisecond
	}
	if c.Suffix == "" {
		c.Suffix = "DMR"
	}
	if c.Slot == 0 {
		c.Slot = 2
	}
	if c.Module == 0 {
		c.Module = 'B'
	}
}

// Bridge is the event loop; create with New and call Run.
type Bridge struct {
	cfg   Config
	dm    DMRNet
	ds    DStarNet
	db    *ids.DB
	log   *slog.Logger
	rng   *rand.Rand
	toDMR *dsCall
	toDS  *dmrCall
	ownDM map[uint32]time.Time
	ownDS map[uint16]time.Time
	now   func() time.Time
	tickN int
	// Stats.
	Calls int
}

// New creates a bridge.
func New(cfg Config, dm DMRNet, ds DStarNet, db *ids.DB, log *slog.Logger) *Bridge {
	cfg.defaults()
	return &Bridge{cfg: cfg, dm: dm, ds: ds, db: db, log: log, rng: rand.New(rand.NewSource(time.Now().UnixNano())),
		ownDM: map[uint32]time.Time{}, ownDS: map[uint16]time.Time{}, now: time.Now}
}

func (b *Bridge) xopt() xcode.Options {
	o := xcode.DefaultOptions
	o.Lookahead = b.cfg.Lookahead
	return o
}

// Run services both networks until ctx ends.
func (b *Bridge) Run(ctx context.Context) error {
	t := time.NewTicker(b.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p := <-b.ds.Recv():
			b.onDStar(p)
		case d := <-b.dm.Recv():
			b.onDMR(d)
		case <-t.C:
			b.tick()
		}
	}
}

func (b *Bridge) busy() bool { return b.toDMR != nil || b.toDS != nil }

// =====================  D-STAR -> DMR  =====================

type dsCall struct {
	streamID  uint16
	call      string
	suffix    string
	src       uint32
	xc        *xcode.Transcoder
	demux     dstar.Demuxer
	text      string
	pos       *dstar.Position
	lcs       []dmr.LC
	lcIdx     int
	frags     [4][4]byte
	pending   [][9]byte
	burstN    int
	dmrStream uint32
	seq       uint8
	out       []hbp.Data
	lastRx    time.Time
	lastSeq   int
	ending    bool
	started   time.Time
	frames    int
}

func (b *Bridge) onDStar(p dstar.Packet) {
	if _, own := b.ownDS[p.StreamID]; own {
		return
	}
	c := b.toDMR
	if c != nil && c.streamID != p.StreamID {
		return // another D-STAR stream while one is active
	}
	if c == nil {
		if b.toDS != nil || p.Last {
			return
		}
		c = b.startToDMR(p)
		if c == nil {
			return
		}
	}
	c.lastRx = b.now()
	if p.Header != nil {
		return
	}
	// Network loss: conceal missing frames (sequence gaps).
	if c.lastSeq >= 0 {
		gap := (int(p.Seq) - c.lastSeq - 1 + 21) % 21
		for i := 0; i < gap && i < 5; i++ {
			b.pushDMRFrame(c, c.xc.Lost())
		}
	}
	c.lastSeq = int(p.Seq)
	if p.Last {
		b.endToDMR(c)
		return
	}
	c.demux.Add(int(p.Seq), p.Data)
	b.refreshMeta(c)
	c.frames++
	b.pushDMRFrame(c, c.xc.Frame(p.AMBE))
}

func (b *Bridge) startToDMR(p dstar.Packet) *dsCall {
	c := &dsCall{streamID: p.StreamID, lastSeq: -1, started: b.now()}
	if p.Header != nil {
		c.call, c.suffix = p.Header.Callsign(), strings.TrimSpace(p.Header.Suffix)
	} else {
		c.call = "UNKNOWN"
	}
	c.src = b.cfg.FallbackID
	if id, ok := b.db.ID(c.call); ok {
		c.src = id
	}
	c.xc = xcode.New(ambe.DStar, ambe.DMR, b.xopt())
	c.dmrStream = b.rng.Uint32() | 1
	b.ownDM[c.dmrStream] = b.now()
	b.buildLCs(c)
	b.toDMR = c
	b.Calls++
	b.log.Info("D-STAR → DMR start", "call", c.call, "suffix", c.suffix, "dmr_src", c.src, "tg", b.cfg.Talkgroup)

	var hdr dmr.Burst
	dmr.EncodeFullLC(c.lcs[0], dmr.DTVoiceHeader, b.cfg.ColourCode, &hdr)
	c.out = append(c.out, b.dmrPacket(c, hbp.FrameDataSync, dmr.DTVoiceHeader, hdr))
	return c
}

// buildLCs sets the embedded LC rotation: voice LC, talker alias, GPS.
func (b *Bridge) buildLCs(c *dsCall) {
	lcs := []dmr.LC{dmr.VoiceLC(false, c.src, b.cfg.Talkgroup)}
	alias := c.call
	if c.suffix != "" {
		alias += " /" + c.suffix
	}
	if c.text != "" {
		alias += " " + c.text
	}
	lcs = append(lcs, dmr.TalkerAliasLCs(alias)...)
	if c.pos != nil {
		lcs = append(lcs, dmr.GPSLC(c.pos.Lat, c.pos.Lon, 7))
	}
	c.lcs = lcs
}

func (b *Bridge) refreshMeta(c *dsCall) {
	changed := false
	if c.demux.Header != nil && c.call == "UNKNOWN" {
		c.call, c.suffix = c.demux.Header.Callsign(), strings.TrimSpace(c.demux.Header.Suffix)
		if id, ok := b.db.ID(c.call); ok {
			c.src = id
		}
		changed = true
	}
	if c.demux.Text != "" && c.demux.Text != c.text {
		c.text = c.demux.Text
		changed = true
		b.log.Info("D-STAR text", "call", c.call, "text", c.text)
	}
	if c.demux.Pos != nil && (c.pos == nil || *c.pos != *c.demux.Pos) {
		p := *c.demux.Pos
		c.pos = &p
		changed = true
		b.log.Info("D-STAR position", "call", c.call, "lat", p.Lat, "lon", p.Lon)
	}
	if changed {
		b.buildLCs(c)
	}
}

func (b *Bridge) dmrPacket(c *dsCall, ft int, dv uint8, burst dmr.Burst) hbp.Data {
	d := hbp.Data{Seq: c.seq, Src: c.src, Dst: b.cfg.Talkgroup, Slot: b.cfg.Slot, FrameType: ft,
		DTypeVSeq: dv, StreamID: c.dmrStream, Burst: burst}
	c.seq++
	return d
}

func (b *Bridge) pushDMRFrame(c *dsCall, f [9]byte) {
	c.pending = append(c.pending, f)
	if len(c.pending) < 3 {
		return
	}
	var frames [3][9]byte
	copy(frames[:], c.pending[:3])
	c.pending = c.pending[3:]
	var bu dmr.Burst
	dmr.SetAMBE(&bu, frames)
	n := c.burstN % 6
	c.burstN++
	if n == 0 {
		dmr.SetSync(&bu, dmr.SyncBSVoice)
		c.out = append(c.out, b.dmrPacket(c, hbp.FrameVoiceSync, 0, bu))
		return
	}
	if n == 1 {
		c.frags = dmr.EmbeddedFragments(c.lcs[c.lcIdx%len(c.lcs)])
		c.lcIdx++
	}
	lcss := [6]uint8{0, 1, 3, 3, 2, 0}[n]
	var frag [4]byte
	if n <= 4 {
		frag = c.frags[n-1]
	}
	dmr.SetEMB(&bu, b.cfg.ColourCode, false, lcss, frag)
	c.out = append(c.out, b.dmrPacket(c, hbp.FrameVoice, uint8(n), bu))
}

var dmrSilence = [9]byte{0xB9, 0xE8, 0x81, 0x52, 0x61, 0x73, 0x00, 0x2A, 0x6B}

func (b *Bridge) endToDMR(c *dsCall) {
	if c.ending {
		return
	}
	if f, ok := c.xc.Flush(); ok {
		b.pushDMRFrame(c, f)
	}
	for len(c.pending) > 0 {
		b.pushDMRFrame(c, dmrSilence)
	}
	var t dmr.Burst
	dmr.EncodeFullLC(c.lcs[0], dmr.DTTerminator, b.cfg.ColourCode, &t)
	c.out = append(c.out, b.dmrPacket(c, hbp.FrameDataSync, dmr.DTTerminator, t))
	c.ending = true
	st := c.xc.Stats
	b.log.Info("D-STAR → DMR end", "call", c.call, "secs", fmt.Sprintf("%.1f", float64(c.frames)*0.02),
		"bad_fec", st.BadFEC, "concealed", st.Concealed+st.PartialConceal, "muted", st.Muted)
}

// =====================  DMR -> D-STAR  =====================

type dsOut struct {
	header *dstar.Header
	seq    uint8
	last   bool
	ambe   [9]byte
	data   [3]byte
}

type dmrCall struct {
	stream   uint32
	src, dst uint32
	call     string
	xc       *xcode.Transcoder
	hdr      dstar.Header
	mux      *dstar.Muxer
	text     string
	gps      string
	ta       dmr.TalkerAlias
	frags    [][4]byte
	dsID     uint16
	dsSeq    uint8
	out      []dsOut
	primed   bool
	lastRx   time.Time
	lastVSeq int
	ending   bool
	frames   int
}

func (b *Bridge) onDMR(d hbp.Data) {
	if _, own := b.ownDM[d.StreamID]; own {
		return
	}
	if d.Slot != b.cfg.Slot || d.Private || d.Dst != b.cfg.Talkgroup {
		return
	}
	c := b.toDS
	if c != nil && c.stream != d.StreamID {
		return
	}
	isTerm := d.FrameType == hbp.FrameDataSync && d.DTypeVSeq == dmr.DTTerminator
	if c == nil {
		if b.toDMR != nil || isTerm || d.FrameType == hbp.FrameDataSync && d.DTypeVSeq != dmr.DTVoiceHeader {
			return
		}
		c = b.startToDS(d)
	}
	c.lastRx = b.now()
	switch {
	case isTerm:
		b.endToDS(c)
	case d.FrameType == hbp.FrameDataSync:
		// header repeat
	default:
		vseq := 0
		if d.FrameType == hbp.FrameVoice {
			vseq = int(d.DTypeVSeq)
		}
		if c.lastVSeq >= 0 {
			gap := (vseq - c.lastVSeq - 1 + 6) % 6
			for i := 0; i < 3*gap; i++ {
				b.pushDSFrame(c, c.xc.Lost())
			}
		}
		c.lastVSeq = vseq
		bu := dmr.Burst(d.Burst)
		if vseq > 0 {
			b.embedded(c, &bu)
		}
		for _, f := range dmr.AMBE(&bu) {
			c.frames++
			b.pushDSFrame(c, c.xc.Frame(f))
		}
	}
}

func (b *Bridge) startToDS(d hbp.Data) *dmrCall {
	c := &dmrCall{stream: d.StreamID, src: d.Src, dst: d.Dst, lastVSeq: -1}
	c.call = b.cfg.GatewayCall
	name := ""
	if e, ok := b.db.Callsign(d.Src); ok {
		c.call, name = e.Callsign, e.Name
	}
	c.text = strings.TrimSpace(fmt.Sprintf("%s %s", c.call, name))
	if c.call == b.cfg.GatewayCall {
		c.text = fmt.Sprintf("DMR ID %d", d.Src)
	}
	c.xc = xcode.New(ambe.DMR, ambe.DStar, b.xopt())
	for c.dsID == 0 {
		c.dsID = uint16(b.rng.Uint32())
	}
	b.ownDS[c.dsID] = b.now()
	c.hdr = dstar.Header{
		RPT1: dstar.Field(b.cfg.GatewayCall, b.cfg.Module), RPT2: dstar.Field(b.cfg.GatewayCall, 'G'),
		YOUR: "CQCQCQ", MY: dstar.Field(c.call, 0), Suffix: b.cfg.Suffix,
	}
	c.mux = dstar.NewMuxer(c.text, &c.hdr, "")
	h := c.hdr
	c.out = append(c.out, dsOut{header: &h})
	b.toDS = c
	b.Calls++
	b.log.Info("DMR → D-STAR start", "src", d.Src, "call", c.call, "tg", d.Dst)
	return c
}

// embedded collects LC fragments (LCSS 1,3,3,2) and acts on complete LCs.
func (b *Bridge) embedded(c *dmrCall, bu *dmr.Burst) {
	_, _, lcss, frag, errs := dmr.EMB(bu)
	if errs > 2 {
		c.frags = nil
		return
	}
	switch lcss {
	case 1:
		c.frags = [][4]byte{frag}
	case 3:
		if len(c.frags) > 0 && len(c.frags) < 3 {
			c.frags = append(c.frags, frag)
		}
	case 2:
		if len(c.frags) == 3 {
			lc, ok := dmr.DecodeEmbedded([4][4]byte{c.frags[0], c.frags[1], c.frags[2], frag})
			if ok {
				b.onLC(c, lc)
			}
		}
		c.frags = nil
	}
}

func (b *Bridge) onLC(c *dmrCall, lc dmr.LC) {
	switch lc.FLCO() {
	case dmr.FLCOTAHeader, dmr.FLCOTABlock1, dmr.FLCOTABlock2, dmr.FLCOTABlock3:
		if s, ok := c.ta.Add(lc); ok && s != "" && s != c.text {
			c.text = s
			b.log.Info("DMR talker alias", "src", c.src, "alias", s)
			c.mux = dstar.NewMuxer(c.text, &c.hdr, c.gps)
		}
	case dmr.FLCOGPSInfo:
		if lat, lon, _, ok := dmr.GPS(lc); ok {
			g := dstar.DPRS(dstar.BaseCall(c.call), dstar.Position{Lat: lat, Lon: lon}, "DMR")
			if g != c.gps {
				c.gps = g
				b.log.Info("DMR position", "src", c.src, "lat", lat, "lon", lon)
				c.mux = dstar.NewMuxer(c.text, &c.hdr, c.gps)
			}
		}
	}
}

func (b *Bridge) pushDSFrame(c *dmrCall, f [9]byte) {
	seq := c.dsSeq % 21
	c.out = append(c.out, dsOut{seq: seq, ambe: f, data: c.mux.Next(int(seq))})
	c.dsSeq = (seq + 1) % 21
}

func (b *Bridge) endToDS(c *dmrCall) {
	if c.ending {
		return
	}
	if f, ok := c.xc.Flush(); ok {
		b.pushDSFrame(c, f)
	}
	c.out = append(c.out, dsOut{seq: c.dsSeq % 21, last: true})
	c.ending = true
	st := c.xc.Stats
	b.log.Info("DMR → D-STAR end", "call", c.call, "secs", fmt.Sprintf("%.1f", float64(c.frames)*0.02),
		"concealed", st.Concealed+st.PartialConceal, "muted", st.Muted)
}

// =====================  pacing and timeouts  =====================

func (b *Bridge) tick() {
	now := b.now()
	b.tickN++
	tickN := b.tickN
	// DMR out: one burst per 3 ticks (60 ms) after one burst of prebuffer.
	if c := b.toDMR; c != nil {
		if !c.ending && now.Sub(c.lastRx) > b.cfg.Timeout {
			b.log.Warn("D-STAR stream timed out", "call", c.call)
			b.endToDMR(c)
		}
		if tickN%3 == 0 && len(c.out) > 0 && (len(c.out) >= 2 || c.ending) {
			if err := b.dm.Send(c.out[0]); err != nil {
				b.log.Debug("dmr send", "err", err)
			}
			c.out = c.out[1:]
		}
		if c.ending && len(c.out) == 0 {
			b.toDMR = nil
		}
	}
	// D-STAR out: header immediately, then one frame per tick after a
	// 4-frame prebuffer (absorbs 60 ms DMR burst arrival).
	if c := b.toDS; c != nil {
		if !c.ending && now.Sub(c.lastRx) > b.cfg.Timeout {
			b.log.Warn("DMR stream timed out", "call", c.call)
			b.endToDS(c)
		}
		for len(c.out) > 0 && c.out[0].header != nil {
			b.ds.SendHeader(c.dsID, *c.out[0].header)
			c.out = c.out[1:]
		}
		if !c.primed && (len(c.out) >= 4 || c.ending) {
			c.primed = true
		}
		if c.primed && len(c.out) > 0 {
			o := c.out[0]
			b.ds.SendVoice(c.dsID, o.seq, o.last, o.ambe, o.data)
			c.out = c.out[1:]
		}
		if c.ending && len(c.out) == 0 {
			b.toDS = nil
		}
	}
	for k, t := range b.ownDM {
		if now.Sub(t) > time.Minute {
			delete(b.ownDM, k)
		}
	}
	for k, t := range b.ownDS {
		if now.Sub(t) > time.Minute {
			delete(b.ownDS, k)
		}
	}
}
