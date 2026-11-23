package main

import (
	"net"
	"testing"
	"time"
)

func testMasterGateway(stage int, addr *net.UDPAddr) *Gateway {
	return &Gateway{masterClients: map[uint32]*DMRMasterClient{
		2341001: {RepeaterID: 2341001, BaseDMRID: 234100, Address: addr, Stage: stage, Connected: time.Now(), LastSeen: time.Now()},
	}}
}

func rpto(id uint32) []byte  { return append([]byte("RPTO"), writeUint32BE(id)...) }
func rptcl(id uint32) []byte { return append([]byte("RPTCL"), writeUint32BE(id)...) }

func TestRPTORequiresCompletedKeyExchange(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 10), Port: 50000}
	g := testMasterGateway(masterWaitingKey, addr) // logged in (RPTL) but no RPTK
	g.handleMasterControl(append(rpto(2341001), []byte("TS1=1;")...), addr)
	if g.authenticatedMasterClient(2341001, addr) != nil {
		t.Fatal("RPTO promoted a client that never completed RPTK")
	}
	if g.masterClients[2341001].Stage != masterWaitingKey {
		t.Fatalf("stage changed to %d", g.masterClients[2341001].Stage)
	}
	g.masterClients[2341001].Stage = masterWaitingConfig // after RPTK
	g.handleMasterControl(append(rpto(2341001), []byte("TS1=1;")...), addr)
	if g.authenticatedMasterClient(2341001, addr) == nil {
		t.Fatal("RPTO after RPTK should still promote the client")
	}
}

func TestRPTOFromOtherAddressDoesNotPromote(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 10), Port: 50000}
	other := &net.UDPAddr{IP: net.IPv4(198, 51, 100, 7), Port: 50000}
	g := testMasterGateway(masterWaitingConfig, addr)
	g.handleMasterControl(rpto(2341001), other)
	if g.masterClients[2341001].Stage != masterWaitingConfig {
		t.Fatal("RPTO from a different address changed the session")
	}
}

func TestRPTCLOnlyFromSessionAddress(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 10), Port: 50000}
	other := &net.UDPAddr{IP: net.IPv4(198, 51, 100, 7), Port: 50000}
	g := testMasterGateway(masterRunning, addr)
	g.handleMasterControl(rptcl(2341001), other)
	if g.masterClients[2341001] == nil {
		t.Fatal("RPTCL from another address closed the session")
	}
	g.handleMasterControl(rptcl(2341001), addr)
	if g.masterClients[2341001] != nil {
		t.Fatal("RPTCL from the session address should close it")
	}
}
