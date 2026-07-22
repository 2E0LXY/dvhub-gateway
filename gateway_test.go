package main

import (
	"strings"
	"testing"
)

func TestDMRFingerprintIgnoresNetworkRewrites(t *testing.T) {
	frameA := make([]byte, 55)
	copy(frameA[:4], "DMRD")
	copy(frameA[5:8], []byte{0x23, 0x51, 0x63})
	copy(frameA[20:], []byte("encoded-voice-payload-that-remains-stable"))
	frameB := append([]byte(nil), frameA...)
	copy(frameB[8:11], []byte{0x00, 0x5b, 0xea})
	copy(frameB[11:20], []byte("rewrite!!"))
	if dmrFingerprint(frameA) != dmrFingerprint(frameB) {
		t.Fatal("fingerprint changed after destination, repeater and stream rewrite")
	}
	frameB[20] ^= 0xff
	if dmrFingerprint(frameA) == dmrFingerprint(frameB) {
		t.Fatal("fingerprint did not change when encoded payload changed")
	}
}

func TestParseRadioIDCSVIncludesIdentityAndLocation(t *testing.T) {
	data := "RADIO_ID,CALLSIGN,FIRST_NAME,LAST_NAME,CITY,STATE,COUNTRY\n2344399,2E0LXY,Example,Operator,Leeds,West Yorkshire,United Kingdom\n"
	users := parseRadioIDCSV(strings.NewReader(data))
	info := users[2344399]
	if info.Callsign != "2E0LXY" || info.Name != "Example Operator" || info.City != "Leeds" || info.State != "West Yorkshire" || info.Country != "United Kingdom" {
		t.Fatalf("unexpected RadioID record: %#v", info)
	}
}

func TestParseDMRTrafficMeta(t *testing.T) {
	frame := make([]byte, 55)
	copy(frame[:4], "DMRD")
	frame[4] = 17
	copy(frame[8:11], []byte{0x00, 0x5b, 0xea})
	copy(frame[11:15], []byte{0x0d, 0xf9, 0xc2, 0xdd})
	frame[15] = 0x90
	copy(frame[16:20], []byte{1, 2, 3, 4})
	frame[53], frame[54] = 2, 73
	meta := parseDMRTrafficMeta(frame)
	if meta.DestinationID != 23530 || meta.Slot != 2 || meta.FrameType != "voice sync" || !meta.BERAvailable || meta.BER != 2 || !meta.RSSIAvailable || meta.RSSI != -73 {
		t.Fatalf("unexpected DMR metadata: %#v", meta)
	}
}

func TestBridgeEndpointsIncludesOptionalConferenceLeg(t *testing.T) {
	pair := bridgeEndpoints(BridgeRoute{ANode: 1, ATG: 23530, BNode: 2, BTG: 23530})
	if len(pair) != 2 {
		t.Fatalf("pair bridge has %d endpoints, want 2", len(pair))
	}
	conference := bridgeEndpoints(BridgeRoute{ANode: 1, ATG: 23530, BNode: 2, BTG: 23530, CNode: 4, CTG: 23530})
	if len(conference) != 3 || conference[4] != 23530 {
		t.Fatalf("conference endpoints = %#v, want TGIF node 4 on TG23530", conference)
	}
}

func TestRegisteredIdentityMustMatchRadioID(t *testing.T) {
	gateway := &Gateway{idDB: map[uint32]RadioIDInfo{2344399: {Callsign: "2E0LXY"}}}
	session := &UserSession{Mode: "DMR", LinkActive: true, AuthStage: authRunning, DMRID: 2344399, Callsign: "2e0lxy"}
	if _, ok, reason := gateway.registeredIdentity(session); !ok {
		t.Fatalf("registered matching identity rejected: %s", reason)
	}
	session.Callsign = "M0ABC"
	if _, ok, _ := gateway.registeredIdentity(session); ok {
		t.Fatal("mismatched callsign was allowed to transmit")
	}
	session.Callsign, session.DMRID = "2E0LXY", 2000000
	if _, ok, _ := gateway.registeredIdentity(session); ok {
		t.Fatal("unregistered DMR ID was allowed to transmit")
	}
}

func TestTXLeaseCanOnlyBeReleasedByOwner(t *testing.T) {
	owner := &WSClient{}
	other := &WSClient{}
	session := &UserSession{ID: 1}
	session.IsActive.Store(true)
	gateway := &Gateway{clients: make(map[*WSClient]bool), txOwner: owner, txNode: 1, txDMRID: 2344399, txCallsign: "2E0LXY"}
	gateway.sessions[0] = session
	if gateway.releaseTX(other, 0, "not owner") {
		t.Fatal("a different client released the TX lease")
	}
	if !session.IsActive.Load() {
		t.Fatal("non-owner release stopped the transmitter")
	}
	if !gateway.releaseTX(owner, 0, "released") {
		t.Fatal("owner could not release TX lease")
	}
	if session.IsActive.Load() || gateway.txOwner != nil {
		t.Fatal("TX lease remained active after owner release")
	}
}
