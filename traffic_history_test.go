package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTrafficHistorySurvivesRestartAndKeepsStreamKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "history.json")
	first := &Gateway{trafficHistoryPath: path}
	for i := 0; i < 60; i++ {
		first.recordTrafficHistory(fmt.Sprint(i), map[string]any{"id": i, "callsign": "M7BMG", "repeater_id": 234201})
	}
	if err := first.persistTrafficHistory(); err != nil {
		t.Fatal(err)
	}
	restarted := &Gateway{trafficHistoryPath: path}
	if err := restarted.restoreTrafficHistory(path); err != nil {
		t.Fatal(err)
	}
	rows := restarted.trafficHistorySnapshot()
	if len(rows) != 50 || rows[0]["id"].(json.Number).String() != "59" || rows[49]["id"].(json.Number).String() != "10" {
		t.Fatalf("history/order lost after restart: %#v", rows)
	}
	restarted.recordTrafficHistory("59", map[string]any{"id": 59, "callsign": "UPDATED"})
	if len(restarted.trafficHistorySnapshot()) != 50 || restarted.trafficHistorySnapshot()[0]["callsign"] != "UPDATED" {
		t.Fatal("restored stream key was not reused")
	}
	if err := restarted.persistTrafficHistory(); err != nil {
		t.Fatal(err)
	}
	third := &Gateway{}
	if err := third.restoreTrafficHistory(path); err != nil || third.trafficHistorySnapshot()[0]["callsign"] != "UPDATED" {
		t.Fatalf("second restart lost updated call: %v", err)
	}
}

func TestInvalidTrafficHistoryDoesNotReplaceMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(path, []byte(`{"version":`), 0600); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{}
	g.recordTrafficHistory("current", map[string]any{"callsign": "CURRENT"})
	if err := g.restoreTrafficHistory(path); err == nil {
		t.Fatal("corrupt history accepted")
	}
	if g.trafficHistorySnapshot()[0]["callsign"] != "CURRENT" {
		t.Fatal("corrupt history replaced valid memory")
	}
}
