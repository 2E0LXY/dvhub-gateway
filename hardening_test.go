package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAnonymousTicketsCannotStarveAuthenticated(t *testing.T) {
	gateway := &Gateway{wsTickets: make(map[string]wsTicket)}
	for i := 0; i < wsTicketMaxAnonymous; i++ {
		if _, err := gateway.issueWSTicket("", "k"+string(rune('A'+i%26))+string(rune('a'+i/26))); err != nil {
			t.Fatalf("anonymous ticket %d: %v", i, err)
		}
	}
	if _, err := gateway.issueWSTicket("", "fresh"); err == nil {
		t.Fatal("anonymous pool should be exhausted")
	}
	if _, err := gateway.issueWSTicket("2E0LXY", "admin"); err != nil {
		t.Fatalf("authenticated ticket blocked by anonymous flood: %v", err)
	}
}

func TestTicketPerClientLimit(t *testing.T) {
	gateway := &Gateway{wsTickets: make(map[string]wsTicket)}
	for i := 0; i < wsTicketMaxPerKey; i++ {
		if _, err := gateway.issueWSTicket("", "1.2.3.4"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gateway.issueWSTicket("", "1.2.3.4"); err == nil {
		t.Fatal("per-client limit not enforced")
	}
	if _, err := gateway.issueWSTicket("", "5.6.7.8"); err != nil {
		t.Fatalf("other client blocked: %v", err)
	}
}

func TestWebSocketOversizedFrameClosesConnection(t *testing.T) {
	gateway := &Gateway{clients: make(map[*WSClient]bool), wsTickets: make(map[string]wsTicket)}
	ticket, _ := gateway.issueWSTicket("", "")
	server := httptest.NewServer(http.HandlerFunc(gateway.handleWS))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?ticket="+ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("a", wsMaxMessageBytes+1)))
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected connection to be closed after oversized frame")
	}
}

func TestLoginLimiterPrunesAndLimits(t *testing.T) {
	a := &clientAccess{loginAttempts: map[string][]time.Time{"old": {time.Now().Add(-2 * time.Minute)}}}
	for i := 0; i < 8; i++ {
		if !a.allowLogin("x") {
			t.Fatalf("attempt %d rejected early", i)
		}
	}
	if a.allowLogin("x") {
		t.Fatal("9th attempt allowed")
	}
	if _, ok := a.loginAttempts["old"]; ok {
		t.Fatal("stale host not pruned")
	}
}

func TestRequestClientKeyAndProxySecret(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	if got := requestClientKey(r); got != "10.0.0.1" {
		t.Fatalf("key %q", got)
	}
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	if got := requestClientKey(r); got != "198.51.100.7" {
		t.Fatalf("key %q", got)
	}
	r.Header.Set("X-Real-IP", "203.0.113.9")
	if got := requestClientKey(r); got != "203.0.113.9" {
		t.Fatalf("key %q", got)
	}
	old := proxySecret
	proxySecret = "s3cret"
	defer func() { proxySecret = old }()
	r.Header.Set("X-DVHub-Authenticated-User", "2e0lxy")
	if authenticatedUser(r) != "" {
		t.Fatal("identity trusted without proxy secret")
	}
	r.Header.Set("X-DVHub-Proxy-Secret", "s3cret")
	if authenticatedUser(r) != "2E0LXY" {
		t.Fatal("identity rejected with valid proxy secret")
	}
}

func TestWriteAtomicFileModeAndNoTmpLeft(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "sub", "secret")
	if err := writeAtomicFile(dest, []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicFile(dest, []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dest)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v err %v", info.Mode(), err)
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %d", len(entries))
	}
}
