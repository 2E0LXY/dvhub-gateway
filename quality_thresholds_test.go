package main

import (
	"testing"
	"time"
)

func TestLossAlertsRequireRepeatedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		loss                      float64
		received, missing, errors uint64
		want                      string
	}{
		{"one drop in tiny sample", 50, 1, 1, 0, "good"},
		{"minor loss", 0.28, 997, 3, 0, "good"},
		{"repeated warning", 1, 297, 3, 0, "warning"},
		{"severe loss", 5, 95, 5, 0, "fault"},
		{"single error", 0, 100, 0, 1, "good"},
		{"repeated errors", 0, 100, 0, 5, "fault"},
		{"no packets", 0, 0, 0, 0, "idle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sampledLossLevel(tc.loss, tc.errors, tc.received, tc.missing, true); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestJitterAloneIsNotStutter(t *testing.T) {
	q := passiveDMRQuality{}
	for i := 0; i < 100; i++ {
		q.events = append(q.events, dmrQualityEvent{At: time.Now(), Received: 1, JitterMS: 55})
	}
	if got := q.snapshot()["stutter_level"]; got != "good" {
		t.Fatalf("zero gaps with jitter: %v", got)
	}
	for i := 0; i < 5; i++ {
		q.events[i].Stutter = true
	}
	if got := q.snapshot()["stutter_level"]; got != "fault" {
		t.Fatalf("5 percent repeated gaps must fault: %v", got)
	}
	for i := range q.events {
		q.events[i].At = time.Now().Add(-31 * time.Second)
	}
	if got := q.snapshot()["level"]; got != "idle" {
		t.Fatalf("old traffic must be neutral: %v", got)
	}
}

func TestVocoderSuccessLatencySeparateFromFailures(t *testing.T) {
	for _, tc := range []struct {
		name            string
		ms              int
		success, failed int
		want            string
	}{
		{"WAN 100ms success", 101, 100, 0, "good"},
		{"one isolated failure", 30, 100, 1, "good"},
		{"repeated warnings", 30, 198, 2, "warning"},
		{"repeated failures", 30, 97, 3, "fault"},
		{"all timeouts", 100, 0, 3, "fault"},
		{"first timeout", 100, 0, 1, "warning"},
		{"slow successful responses", 160, 20, 0, "warning"},
		{"very slow successful responses", 310, 20, 0, "fault"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := passiveVocoderQuality{}
			for i := 0; i < tc.success; i++ {
				q.observe(time.Duration(tc.ms)*time.Millisecond, true)
			}
			for i := 0; i < tc.failed; i++ {
				q.observe(100*time.Millisecond, false)
			}
			s := q.snapshot()
			if s["level"] != tc.want {
				t.Fatalf("got %#v want %s", s, tc.want)
			}
			if tc.success == 0 && s["latency_p95_ms"] != float64(0) {
				t.Fatal("failed timeouts must not become successful latency samples")
			}
		})
	}
	q := passiveVocoderQuality{samples: []vocoderQualitySample{{At: time.Now().Add(-31 * time.Second), LatencyMS: 100, Success: false}}}
	if q.snapshot()["level"] != "idle" {
		t.Fatal("stale samples must be neutral")
	}
}

func TestNetworkStaleAndResetCounters(t *testing.T) {
	q := passiveNetworkQuality{}
	q.observe(networkCounterSample{Interface: "eth0", Packets: 100, Errors: 10, At: time.Now().Add(-40 * time.Second)})
	q.observe(networkCounterSample{Interface: "eth0", Packets: 200, Errors: 15, At: time.Now().Add(-35 * time.Second)})
	if q.snapshot()["level"] != "idle" {
		t.Fatal("stale interface samples must be neutral")
	}
	q.observe(networkCounterSample{Interface: "eth0", Packets: 300, Errors: 0, At: time.Now()})
	if q.snapshot()["available"] != false {
		t.Fatal("reset errors counter must not underflow")
	}
}
