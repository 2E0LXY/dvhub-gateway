package main

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestConferenceConfigYSFOptional(t *testing.T) {
	base := `"enabled":true,"callsign":"2E0LXY","bridge_dmr_id":2344399,"bridge_essid":1,"brandmeister_password":"a","tgif_password":"b"`
	cases := []struct {
		name, json string
		ok, ysf    bool
	}{
		{"ysf default needs ids", `{` + base + `}`, false, true},
		{"ysf default with ids", `{` + base + `,"ysf_dmr_id":2344398}`, true, true},
		{"ysf id equal to bridge id", `{` + base + `,"ysf_dmr_id":2344399}`, false, true},
		{"ysf disabled needs no id", `{` + base + `,"ysf_enabled":false}`, true, false},
		{"missing tgif password", `{"enabled":true,"callsign":"2E0LXY","bridge_dmr_id":2344399,"brandmeister_password":"a","ysf_enabled":false}`, false, false},
	}
	for _, tc := range cases {
		config, err := parseYorkshireConferenceConfig([]byte(tc.json))
		if (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", tc.name, err, tc.ok)
			continue
		}
		if err == nil && config.ysfEnabled() != tc.ysf {
			t.Errorf("%s: ysfEnabled=%v, want %v", tc.name, config.ysfEnabled(), tc.ysf)
		}
	}
}

func testDMRD(dst uint32, stream uint32) []byte {
	frame := make([]byte, 55)
	copy(frame, "DMRD")
	frame[5], frame[6], frame[7] = 0x23, 0x0A, 0x4F
	frame[8], frame[9], frame[10] = byte(dst>>16), byte(dst>>8), byte(dst)
	binary.BigEndian.PutUint32(frame[11:15], 2344399)
	binary.BigEndian.PutUint32(frame[16:20], stream)
	for index := 20; index < 53; index++ {
		frame[index] = byte(index)
	}
	return frame
}

func TestLocalMasterBridgesOnlyItsTalkgroup(t *testing.T) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	sender, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	gateway := &Gateway{masterClients: map[uint32]*DMRMasterClient{}}
	session := &UserSession{ID: 2, Mode: "DMR", Target: "BrandMeister-UK-2341", TG: 23530, RepeaterID: 234439901,
		LinkActive: true, AuthStage: authRunning, Conn: sender, RemoteAddr: listener.LocalAddr().(*net.UDPAddr)}
	for index := range gateway.sessions {
		gateway.sessions[index] = &UserSession{ID: index + 1}
	}
	gateway.sessions[1] = session
	gateway.bridge = BridgeRoute{Active: true, ANode: 1, ATG: 23530, BNode: 2, BTG: 23530,
		Suppress: map[int]BridgeSuppression{}, Fingerprints: map[[32]byte]time.Time{}}

	read := func() []byte {
		listener.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		buffer := make([]byte, 128)
		n, _, readErr := listener.ReadFromUDP(buffer)
		if readErr != nil {
			return nil
		}
		return buffer[:n]
	}

	// A hotspot sending any other talkgroup must never reach the networks.
	gateway.forwardBridgeFrameFrom(-1, 9, 2344399, testDMRD(9, 100))
	if got := read(); got != nil {
		t.Fatalf("TG9 from the local master leaked to a network: % x", got[:12])
	}

	// TG23530 is forwarded with the destination and repeater ID rewritten.
	gateway.forwardBridgeFrameFrom(-1, LocalMasterTG, 2344399, testDMRD(LocalMasterTG, 101))
	got := read()
	if got == nil {
		t.Fatal("TG23530 from the local master was not forwarded")
	}
	if dmrDestination(got) != 23530 || binary.BigEndian.Uint32(got[11:15]) != 234439901 {
		t.Fatalf("forwarded frame not rewritten: dst=%d rpt=%d", dmrDestination(got), binary.BigEndian.Uint32(got[11:15]))
	}
}
