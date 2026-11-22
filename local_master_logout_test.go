package main

import (
	"net"
	"testing"
	"time"
)

func TestLocalMasterLogoutRequiresSamePeer(t *testing.T) {
	const repeaterID uint32 = 234439901
	owner := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 10), Port: 49000}
	otherIP := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 11), Port: 49000}
	otherPort := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 10), Port: 49001}
	g := &Gateway{masterClients: map[uint32]*DMRMasterClient{
		repeaterID: {RepeaterID: repeaterID, Address: owner, Stage: masterRunning, LastSeen: time.Now()},
	}}
	packet := append([]byte("RPTCL"), writeUint32BE(repeaterID)...)

	for _, peer := range []*net.UDPAddr{otherIP, otherPort} {
		if !g.handleMasterControl(packet, peer) {
			t.Fatalf("RPTCL from %s was not handled", peer)
		}
		if g.masterClients[repeaterID] == nil {
			t.Fatalf("RPTCL from %s removed another peer's session", peer)
		}
	}
	if !g.handleMasterControl(packet, owner) || g.masterClients[repeaterID] != nil {
		t.Fatal("RPTCL from the owning peer did not remove its session")
	}
}

func TestLocalMasterOptionsCannotBypassKey(t *testing.T) {
	const repeaterID uint32 = 234439901
	owner := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 10), Port: 49000}
	other := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 11), Port: 49000}
	client := &DMRMasterClient{RepeaterID: repeaterID, Address: owner, Stage: masterWaitingKey, LastSeen: time.Now()}
	g := &Gateway{masterClients: map[uint32]*DMRMasterClient{repeaterID: client}}
	packet := append([]byte("RPTO"), writeUint32BE(repeaterID)...)

	if !g.handleMasterControl(packet, owner) || client.Stage != masterWaitingKey {
		t.Fatal("RPTO bypassed the shared-secret key stage")
	}
	client.Stage = masterWaitingConfig // the RPTK secret has been accepted
	if !g.handleMasterControl(packet, other) || client.Stage != masterWaitingConfig {
		t.Fatal("RPTO from a different UDP peer advanced the session")
	}
	if !g.handleMasterControl(packet, owner) || client.Stage != masterRunning {
		t.Fatal("RPTO after key authentication did not advance the owner")
	}
}
