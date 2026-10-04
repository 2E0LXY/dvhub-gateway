// Package ids maps DMR radio IDs to callsigns and back. It reads the
// DMRIds.dat format used by MMDVMHost (id<TAB>callsign[<TAB>name...]) and
// radioid.net CSV exports (RADIO_ID,CALLSIGN,FIRST_NAME,...).
package ids

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Entry is one registration.
type Entry struct {
	ID       uint32
	Callsign string
	Name     string
}

// DB is safe for concurrent use; Load replaces the contents atomically.
type DB struct {
	mu     sync.RWMutex
	byID   map[uint32]Entry
	byCall map[string]uint32
	extra  map[string]uint32
}

// New returns an empty DB with optional fixed callsign→ID overrides.
func New(overrides map[string]uint32) *DB {
	o := map[string]uint32{}
	for k, v := range overrides {
		o[strings.ToUpper(k)] = v
	}
	return &DB{byID: map[uint32]Entry{}, byCall: map[string]uint32{}, extra: o}
}

// LoadFile reads path (format auto-detected per line).
func (d *DB) LoadFile(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return d.Load(f)
}

// Load reads entries from r. The first ID seen for a callsign wins.
func (d *DB) Load(r io.Reader) (int, error) {
	byID := map[uint32]Entry{}
	byCall := map[string]uint32{}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 1<<16), 1<<20)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		var f []string
		if strings.Contains(line, "\t") {
			f = strings.Split(line, "\t")
		} else if strings.Contains(line, ",") {
			f = strings.Split(line, ",")
		} else {
			f = strings.Fields(line)
		}
		if len(f) < 2 {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSpace(f[0]), 10, 32)
		if err != nil || id == 0 || id > 0xFFFFFF {
			continue // header row or junk
		}
		call := strings.ToUpper(strings.TrimSpace(f[1]))
		if call == "" {
			continue
		}
		e := Entry{ID: uint32(id), Callsign: call}
		if len(f) > 2 {
			e.Name = strings.TrimSpace(f[2])
		}
		byID[e.ID] = e
		if _, ok := byCall[call]; !ok {
			byCall[call] = e.ID
		}
	}
	if err := s.Err(); err != nil {
		return 0, err
	}
	d.mu.Lock()
	d.byID, d.byCall = byID, byCall
	d.mu.Unlock()
	return len(byID), nil
}

// Callsign returns the callsign for an ID.
func (d *DB) Callsign(id uint32) (Entry, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.byID[id]
	return e, ok
}

// ID returns the DMR ID for a callsign (overrides first).
func (d *DB) ID(call string) (uint32, bool) {
	call = strings.ToUpper(strings.TrimSpace(call))
	d.mu.RLock()
	defer d.mu.RUnlock()
	if id, ok := d.extra[call]; ok {
		return id, true
	}
	id, ok := d.byCall[call]
	return id, ok
}
