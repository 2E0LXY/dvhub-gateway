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
