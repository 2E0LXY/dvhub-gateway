// dvxbridge links a DMR talkgroup (HomeBrew master) to a D-STAR reflector
// module (DExtra), transcoding voice and translating callsign, talker
// alias, text and GPS.
//
//	dvxbridge -config /etc/dvxbridge.ini
//
// SIGHUP reloads the DMR ID database.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/2E0LXY/dvxcode/bridge"
	"github.com/2E0LXY/dvxcode/dstar"
	"github.com/2E0LXY/dvxcode/hbp"
	"github.com/2E0LXY/dvxcode/ids"
)

type ini map[string]map[string]string

func readINI(path string) (ini, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := ini{}
	sec := ""
	s := bufio.NewScanner(f)
	for n := 1; s.Scan(); n++ {
		l := strings.TrimSpace(s.Text())
		if l == "" || l[0] == '#' || l[0] == ';' {
			continue
		}
		if l[0] == '[' && l[len(l)-1] == ']' {
			sec = strings.ToLower(l[1 : len(l)-1])
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key = value", path, n)
		}
		if out[sec] == nil {
			out[sec] = map[string]string{}
		}
		for _, mark := range []string{" ;", "\t;", " #", "\t#"} {
			if i := strings.Index(v, mark); i >= 0 {
				v = v[:i] // inline comment
			}
		}
		out[sec][strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out, s.Err()
}

func (c ini) str(sec, key, def string) string {
	if v, ok := c[sec][key]; ok && v != "" {
		return v
	}
	return def
}

func (c ini) num(sec, key string, def uint64) uint64 {
	v := c.str(sec, key, "")
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		fatal(fmt.Errorf("[%s] %s: %v", sec, key, err))
	}
	return n
}

func (c ini) float(sec, key string) float64 {
	v, _ := strconv.ParseFloat(c.str(sec, key, "0"), 64)
	return v
}

// serveStatus exposes /status (JSON counters) and /healthz (200 when both
// network links are up, else 503) for monitoring. Bind to localhost and put
// Caddy in front if it must be reachable remotely.
func serveStatus(ctx context.Context, addr string, b *bridge.Bridge, dm *hbp.Client, ds *dstar.DExtra, log *slog.Logger) {
	start := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Uptime  string          `json:"uptime"`
			HBPUp   bool            `json:"hbp_up"`
			DExtra  bool            `json:"dextra_up"`
			Counter bridge.Counters `json:"counters"`
		}{time.Since(start).Round(time.Second).String(), dm.Up(), ds.Up(), b.Snapshot()})
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if dm.Up() && ds.Up() {
			w.Write([]byte("ok\n"))
			return
		}
		http.Error(w, "link down", http.StatusServiceUnavailable)
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("status server", "err", err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "dvxbridge:", err)
	os.Exit(1)
}

func main() {
	path := flag.String("config", "/etc/dvxbridge.ini", "configuration file")
	debug := flag.Bool("debug", false, "debug logging")
	flag.Parse()
	c, err := readINI(*path)
	if err != nil {
		fatal(err)
	}
	lvl := slog.LevelInfo
	if *debug {
		lvl = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))

	gw := strings.ToUpper(c.str("general", "callsign", ""))
	if gw == "" {
		fatal(fmt.Errorf("[general] callsign is required"))
	}
	overrides := map[string]uint32{}
	for k, v := range c["idoverride"] {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			fatal(fmt.Errorf("[idoverride] %s: %v", k, err))
		}
		overrides[k] = uint32(n)
	}
	db := ids.New(overrides)
	idPath := c.str("general", "dmrids", "/var/lib/dvxbridge/DMRIds.dat")
	load := func() {
		if n, err := db.LoadFile(idPath); err != nil {
			log.Warn("DMR ID database not loaded; callsigns will use fallback", "file", idPath, "err", err)
		} else {
			log.Info("DMR ID database loaded", "file", idPath, "entries", n)
		}
	}
	load()

	hc := hbp.Config{
		Master: c.str("dmr", "master", ""), ID: uint32(c.num("dmr", "id", 0)), Password: c.str("dmr", "password", ""),
		Callsign: gw, ColourCode: int(c.num("dmr", "colourcode", 1)), Slots: 3,
		RXFreq: uint32(c.num("dmr", "rxfrequency", 0)), TXFreq: uint32(c.num("dmr", "txfrequency", 0)),
		Lat: c.float("general", "latitude"), Lon: c.float("general", "longitude"),
		Location: c.str("general", "location", ""), Description: "D-STAR bridge (dvxcode)",
		URL: c.str("general", "url", ""), Options: c.str("dmr", "options", ""),
		Software: "dvxcode", Package: "dvxbridge",
	}
	if hc.Master == "" || hc.ID == 0 {
		fatal(fmt.Errorf("[dmr] master and id are required"))
	}
	mod := strings.ToUpper(c.str("dstar", "module", "C"))
	lmod := strings.ToUpper(c.str("dstar", "localmodule", "B"))
	xc := dstar.DExtraConfig{Reflector: c.str("dstar", "reflector", ""), Callsign: gw, LocalModule: lmod[0], ReflectorModule: mod[0]}
	if xc.Reflector == "" {
		fatal(fmt.Errorf("[dstar] reflector is required (host:30001)"))
	}
	bc := bridge.Config{
		Slot: int(c.num("dmr", "slot", 2)), Talkgroup: uint32(c.num("dmr", "talkgroup", 0)),
		ColourCode: uint8(c.num("dmr", "colourcode", 1)), FallbackID: uint32(c.num("dmr", "fallbackid", uint64(hc.ID))),
		GatewayCall: gw, Module: lmod[0], Suffix: c.str("dstar", "suffix", "DMR"),
		Lookahead: c.str("general", "lowlatency", "0") != "1",
	}
	if bc.Talkgroup == 0 {
		fatal(fmt.Errorf("[dmr] talkgroup is required"))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			load()
		}
	}()

	dm := hbp.New(hc, log)
	ds := dstar.NewDExtra(xc, log)
	go dm.Run(ctx)
	go ds.Run(ctx)
	log.Info("dvxbridge starting", "dmr_tg", bc.Talkgroup, "slot", bc.Slot, "reflector", xc.Reflector, "module", string(xc.ReflectorModule))
	b := bridge.New(bc, dm, ds, db, log)
	if addr := c.str("general", "status", ""); addr != "" {
		go serveStatus(ctx, addr, b, dm, ds, log)
	}
	b.Run(ctx)
	time.Sleep(200 * time.Millisecond) // let unlink/RPTCL go out
}
