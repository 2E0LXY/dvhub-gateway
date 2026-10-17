package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

type storedTrafficHistoryEntry struct {
	Key  string         `json:"key"`
	Data map[string]any `json:"data"`
}

type storedTrafficHistory struct {
	Version int                         `json:"version"`
	Entries []storedTrafficHistoryEntry `json:"entries"`
}

func (g *Gateway) restoreTrafficHistory(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var stored storedTrafficHistory
	decoder := json.NewDecoder(io.LimitReader(f, 1024*1024))
	decoder.UseNumber()
	if err := decoder.Decode(&stored); err != nil {
		return err
	}
	if stored.Version != 1 {
		return fmt.Errorf("unsupported activity history version %d", stored.Version)
	}
	entries := make([]trafficHistoryEntry, 0, 50)
	seen := make(map[string]bool)
	for _, entry := range stored.Entries {
		if entry.Key == "" || entry.Data == nil || seen[entry.Key] {
			continue
		}
		seen[entry.Key] = true
		entries = append(entries, trafficHistoryEntry{key: entry.Key, data: entry.Data})
		if len(entries) == 50 {
			break
		}
	}
	g.trafficMu.Lock()
	g.trafficHistory = entries
	g.trafficMu.Unlock()
	return nil
}

func (g *Gateway) startTrafficHistoryPersistence(path string) {
	g.trafficHistoryPath = path
	if err := g.restoreTrafficHistory(path); err != nil {
		log.Printf("[ACTIVITY] Could not restore history: %v", err)
	}
	g.trafficSaveRequests = make(chan struct{}, 1)
	go func() {
		for range g.trafficSaveRequests {
			// Coalesce media updates on a separate worker, keeping disk I/O
			// out of the packet-handling path. Shutdown explicitly flushes.
			time.Sleep(250 * time.Millisecond)
			if err := g.persistTrafficHistory(); err != nil {
				log.Printf("[ACTIVITY] Could not save history: %v", err)
			}
		}
	}()
}

// Called with trafficMu held; notification is bounded and never blocks audio.
func (g *Gateway) requestTrafficHistorySave() {
	if g.trafficSaveRequests == nil {
		return
	}
	select {
	case g.trafficSaveRequests <- struct{}{}:
	default:
	}
}

func (g *Gateway) persistTrafficHistory() error {
	if g.trafficHistoryPath == "" {
		return nil
	}
	// Serialize writers before taking the snapshot, so an older worker
	// cannot overwrite a newer shutdown snapshot.
	g.trafficSaveMu.Lock()
	defer g.trafficSaveMu.Unlock()
	g.trafficMu.Lock()
	stored := storedTrafficHistory{Version: 1, Entries: make([]storedTrafficHistoryEntry, 0, len(g.trafficHistory))}
	for _, entry := range g.trafficHistory {
		stored.Entries = append(stored.Entries, storedTrafficHistoryEntry{Key: entry.key, Data: entry.data})
	}
	body, err := json.Marshal(stored)
	g.trafficMu.Unlock()
	if err != nil {
		return err
	}
	// This helper syncs a 0600 temporary file and atomically replaces the
	// destination; a failed write leaves the last committed history intact.
	return persistPrivateSetting(g.trafficHistoryPath, string(body))
}
