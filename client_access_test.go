package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestUSRPVoicePacket(t *testing.T) {
	pcm := make([]byte, 320)
	for i := range pcm {
		pcm[i] = byte(i)
	}
	packet := buildUSRPVoice(42, pcm)
	if len(packet) != 352 || string(packet[:4]) != "USRP" {
		t.Fatalf("invalid USRP packet")
	}
	if binary.BigEndian.Uint32(packet[4:8]) != 42 || packet[15] != 1 || packet[20] != 0 {
		t.Fatalf("invalid USRP header")
	}
	if !bytes.Equal(packet[32:], pcm) {
		t.Fatalf("PCM payload changed")
	}
	eot := buildUSRPEOT(43)
	if len(eot) != 32 || eot[15] != 0 {
		t.Fatalf("invalid EOT packet")
	}
}

func TestUSRPIdentityPacket(t *testing.T) {
	packet := buildUSRPIdentity(7, clientSession{Callsign: "2E0LXY", DMRID: 2344399})
	if len(packet) != 352 || packet[20] != 2 || packet[32] != 8 {
		t.Fatalf("invalid identity packet")
	}
	source := uint32(packet[34])<<16 | uint32(packet[35])<<8 | uint32(packet[36])
	target := uint32(packet[41])<<16 | uint32(packet[42])<<8 | uint32(packet[43])
	if source != 2344399 || target != 23530 || string(packet[46:52]) != "2E0LXY" {
		t.Fatalf("identity metadata incorrect: source=%d target=%d call=%q", source, target, packet[46:52])
	}
}

func TestClientAccountLoginAndTicket(t *testing.T) {
	gateway := &Gateway{idDB: map[uint32]RadioIDInfo{2344399: {Callsign: "2E0LXY"}}}
	access := newClientAccess(gateway)
	access.path = filepath.Join(t.TempDir(), "users.json")

	create := httptest.NewRequest(http.MethodPost, "/api/client/users", bytes.NewBufferString(`{"callsign":"2E0LXY","dmr_id":2344399,"password":"correct-horse-battery","enabled":true}`))
	create.Header.Set("X-DVHub-Authenticated-User", "2E0LXY")
	create.Header.Set("X-DVHub-Control", "1")
	created := httptest.NewRecorder()
	access.handleUsers(created, create)
	if created.Code != http.StatusOK {
		t.Fatalf("create status %d: %s", created.Code, created.Body.String())
	}

	login := httptest.NewRequest(http.MethodPost, "/api/client/login", bytes.NewBufferString(`{"callsign":"2E0LXY","dmr_id":2344399,"password":"correct-horse-battery"}`))
	login.RemoteAddr = "192.0.2.5:1234"
	loggedIn := httptest.NewRecorder()
	access.handleLogin(loggedIn, login)
	if loggedIn.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", loggedIn.Code, loggedIn.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(loggedIn.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	token, _ := result["token"].(string)
	if len(token) != 64 {
		t.Fatalf("missing session token")
	}

	ticketRequest := httptest.NewRequest(http.MethodPost, "/api/client/voice_ticket", bytes.NewBufferString(`{}`))
	ticketRequest.Header.Set("Authorization", "Bearer "+token)
	ticketResponse := httptest.NewRecorder()
	access.handleVoiceTicket(ticketResponse, ticketRequest)
	if ticketResponse.Code != http.StatusOK {
		t.Fatalf("ticket status %d: %s", ticketResponse.Code, ticketResponse.Body.String())
	}
}

func TestClientAccountRejectsMismatchedRadioID(t *testing.T) {
	gateway := &Gateway{idDB: map[uint32]RadioIDInfo{2344399: {Callsign: "OTHER"}}}
	access := newClientAccess(gateway)
	access.path = filepath.Join(t.TempDir(), "users.json")
	request := httptest.NewRequest(http.MethodPost, "/api/client/users", bytes.NewBufferString(`{"callsign":"2E0LXY","dmr_id":2344399,"password":"correct-horse-battery"}`))
	request.Header.Set("X-DVHub-Authenticated-User", "2E0LXY")
	request.Header.Set("X-DVHub-Control", "1")
	response := httptest.NewRecorder()
	access.handleUsers(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected mismatch rejection, got %d", response.Code)
	}
}
