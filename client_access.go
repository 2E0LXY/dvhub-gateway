package main

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
)

const (
	clientUsersPath       = "/var/lib/dvgateway/client-users.json"
	clientVoiceListenAddr = "127.0.0.1:34018"
	clientVoiceASLAddr    = "127.0.0.1:32018"
	clientSessionLifetime = 12 * time.Hour
	clientVoiceMaxCall    = 3 * time.Minute
)

type clientUserRecord struct {
	Callsign     string `json:"callsign"`
	DMRID        uint32 `json:"dmr_id"`
	PasswordHash string `json:"password_hash"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    string `json:"created_at"`
}

type clientUserPublic struct {
	Callsign  string `json:"callsign"`
	DMRID     uint32 `json:"dmr_id"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

type clientSession struct {
	Callsign string
	DMRID    uint32
	Expires  time.Time
}

type voiceTicket struct {
	Session clientSession
	Expires time.Time
}

type clientAccess struct {
	gw            *Gateway
	path          string
	mu            sync.Mutex
	users         map[string]clientUserRecord
	sessions      map[string]clientSession
	voiceTickets  map[string]voiceTicket
	loginAttempts map[string][]time.Time
	voice         *voiceBridge
}

func newClientAccess(gw *Gateway) *clientAccess {
	access := &clientAccess{
		gw: gw, path: clientUsersPath,
		users: make(map[string]clientUserRecord), sessions: make(map[string]clientSession),
		voiceTickets: make(map[string]voiceTicket), loginAttempts: make(map[string][]time.Time),
	}
	if err := access.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Printf("[CLIENT] Unable to load user accounts: %v\n", err)
	}
	return access
}

func (a *clientAccess) registerHandlers() {
	http.HandleFunc("/api/client/login", a.handleLogin)
	http.HandleFunc("/api/client/session", a.handleSession)
	http.HandleFunc("/api/client/voice_ticket", a.handleVoiceTicket)
	http.HandleFunc("/api/client/users", a.handleUsers)
	http.HandleFunc("/voice", a.handleVoice)
}

func (a *clientAccess) startVoice() error {
	bridge, err := newVoiceBridge(a.gw, clientVoiceListenAddr, clientVoiceASLAddr)
	if err != nil {
		return err
	}
	a.voice = bridge
	return nil
}

func (a *clientAccess) load() error {
	data, err := os.ReadFile(a.path)
	if err != nil {
		return err
	}
	var records []clientUserRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return err
	}
	for _, record := range records {
		record.Callsign = strings.ToUpper(strings.TrimSpace(record.Callsign))
		if validCallsign.MatchString(record.Callsign) && record.DMRID > 0 && record.PasswordHash != "" {
			a.users[record.Callsign] = record
		}
	}
	return nil
}

func (a *clientAccess) saveLocked() error {
	records := make([]clientUserRecord, 0, len(a.users))
	for _, record := range a.users {
		records = append(records, record)
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0750); err != nil {
		return err
	}
	tmp := a.path + ".new"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, a.path)
}

func secureClientToken() (string, error) {
	data := make([]byte, 32)
	if _, err := cryptorand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func bearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < 8 || !strings.EqualFold(value[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(value[7:])
}

func (a *clientAccess) authenticatedSession(r *http.Request) (clientSession, bool) {
	token := bearerToken(r)
	if len(token) != 64 {
		return clientSession{}, false
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[token]
	if !ok || !session.Expires.After(now) {
		delete(a.sessions, token)
		return clientSession{}, false
	}
	user, ok := a.users[session.Callsign]
	if !ok || !user.Enabled || user.DMRID != session.DMRID {
		delete(a.sessions, token)
		return clientSession{}, false
	}
	return session, true
}

func jsonResponse(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (a *clientAccess) allowLogin(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	a.mu.Lock()
	defer a.mu.Unlock()
	recent := a.loginAttempts[host][:0]
	for _, attempt := range a.loginAttempts[host] {
		if attempt.After(cutoff) {
			recent = append(recent, attempt)
		}
	}
	if len(recent) >= 8 {
		a.loginAttempts[host] = recent
		return false
	}
	a.loginAttempts[host] = append(recent, now)
	return true
}

func (a *clientAccess) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	remote := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	if remote == "" {
		remote = r.RemoteAddr
	}
	if !a.allowLogin(remote) {
		jsonResponse(w, http.StatusTooManyRequests, map[string]string{"error": "Too many login attempts"})
		return
	}
	var request struct {
		Callsign string `json:"callsign"`
		DMRID    uint32 `json:"dmr_id"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}
	request.Callsign = strings.ToUpper(strings.TrimSpace(request.Callsign))
	a.mu.Lock()
	user, ok := a.users[request.Callsign]
	a.mu.Unlock()
	if !ok || !user.Enabled || user.DMRID != request.DMRID || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(request.Password)) != nil {
		time.Sleep(150 * time.Millisecond)
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": "Invalid callsign, DMR ID or password"})
		return
	}
	token, err := secureClientToken()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "Unable to create session"})
		return
	}
	session := clientSession{Callsign: user.Callsign, DMRID: user.DMRID, Expires: time.Now().Add(clientSessionLifetime)}
	a.mu.Lock()
	a.sessions[token] = session
	a.pruneLocked()
	a.mu.Unlock()
	jsonResponse(w, http.StatusOK, map[string]any{"token": token, "role": "user", "callsign": session.Callsign, "dmr_id": session.DMRID, "expires_at": session.Expires.UTC().Format(time.RFC3339)})
}

func (a *clientAccess) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	session, ok := a.authenticatedSession(r)
	if !ok {
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": "Authentication required"})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"role": "user", "callsign": session.Callsign, "dmr_id": session.DMRID, "expires_at": session.Expires.UTC().Format(time.RFC3339)})
}

func (a *clientAccess) handleVoiceTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	session, ok := a.authenticatedSession(r)
	if !ok {
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": "Authentication required"})
		return
	}
	token, err := secureClientToken()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "Unable to create voice ticket"})
		return
	}
	a.mu.Lock()
	a.voiceTickets[token] = voiceTicket{Session: session, Expires: time.Now().Add(30 * time.Second)}
	a.pruneLocked()
	a.mu.Unlock()
	jsonResponse(w, http.StatusOK, map[string]any{"ticket": token, "expires_in_seconds": 30})
}

func (a *clientAccess) pruneLocked() {
	now := time.Now()
	for token, session := range a.sessions {
		if !session.Expires.After(now) {
			delete(a.sessions, token)
		}
	}
	for token, ticket := range a.voiceTickets {
		if !ticket.Expires.After(now) {
			delete(a.voiceTickets, token)
		}
	}
}

func (a *clientAccess) consumeVoiceTicket(token string) (clientSession, bool) {
	if len(token) != 64 {
		return clientSession{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ticket, ok := a.voiceTickets[token]
	delete(a.voiceTickets, token)
	if !ok || !ticket.Expires.After(time.Now()) {
		return clientSession{}, false
	}
	return ticket.Session, true
}

func (a *clientAccess) handleUsers(w http.ResponseWriter, r *http.Request) {
	if authenticatedUser(r) == "" {
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": "Authentication required"})
		return
	}
	if r.Method != http.MethodGet && !requireControlAuth(w, r) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		users := make([]clientUserPublic, 0, len(a.users))
		for _, u := range a.users {
			users = append(users, clientUserPublic{u.Callsign, u.DMRID, u.Enabled, u.CreatedAt})
		}
		voiceStatus := map[string]any{"ready": false, "clients": 0, "transmitting": false, "node": 1997, "conference_node": 530471}
		if a.voice != nil {
			voiceStatus = a.voice.status()
		}
		jsonResponse(w, http.StatusOK, map[string]any{"users": users, "status": voiceStatus})
	case http.MethodPost:
		var request struct {
			Callsign string `json:"callsign"`
			DMRID    uint32 `json:"dmr_id"`
			Password string `json:"password"`
			Enabled  *bool  `json:"enabled"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&request); err != nil {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
			return
		}
		request.Callsign = strings.ToUpper(strings.TrimSpace(request.Callsign))
		if !validCallsign.MatchString(request.Callsign) || request.DMRID < 1000000 || request.DMRID > 9999999 {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "A valid callsign and 7-digit DMR ID are required"})
			return
		}
		a.gw.dbMutex.RLock()
		info, found := a.gw.idDB[request.DMRID]
		a.gw.dbMutex.RUnlock()
		if !found || !strings.EqualFold(strings.TrimSpace(info.Callsign), request.Callsign) {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "The DMR ID is not registered to that callsign"})
			return
		}
		existing, exists := a.users[request.Callsign]
		hash := existing.PasswordHash
		if request.Password != "" {
			if len(request.Password) < 10 {
				jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "Password must be at least 10 characters"})
				return
			}
			encoded, err := bcrypt.GenerateFromPassword([]byte(request.Password), 12)
			if err != nil {
				jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "Unable to secure password"})
				return
			}
			hash = string(encoded)
		}
		if hash == "" {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "Password is required for a new account"})
			return
		}
		enabled := true
		if exists {
			enabled = existing.Enabled
		}
		if request.Enabled != nil {
			enabled = *request.Enabled
		}
		created := existing.CreatedAt
		if created == "" {
			created = time.Now().UTC().Format(time.RFC3339)
		}
		a.users[request.Callsign] = clientUserRecord{Callsign: request.Callsign, DMRID: request.DMRID, PasswordHash: hash, Enabled: enabled, CreatedAt: created}
		if err := a.saveLocked(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "Unable to save account"})
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"saved": true, "callsign": request.Callsign, "dmr_id": request.DMRID, "enabled": enabled})
	case http.MethodDelete:
		callsign := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("callsign")))
		if _, ok := a.users[callsign]; !ok {
			jsonResponse(w, http.StatusNotFound, map[string]string{"error": "Account not found"})
			return
		}
		delete(a.users, callsign)
		for token, session := range a.sessions {
			if session.Callsign == callsign {
				delete(a.sessions, token)
			}
		}
		if err := a.saveLocked(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "Unable to save accounts"})
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}

func (a *clientAccess) handleVoice(w http.ResponseWriter, r *http.Request) {
	session, ok := a.consumeVoiceTicket(r.URL.Query().Get("ticket"))
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if a.voice == nil {
		http.Error(w, "voice service unavailable", http.StatusServiceUnavailable)
		return
	}
	a.voice.serve(w, r, session)
}

type voiceClient struct {
	conn      *websocket.Conn
	session   clientSession
	send      chan WSMessage
	done      chan struct{}
	closeOnce sync.Once
}

func (c *voiceClient) close() { c.closeOnce.Do(func() { close(c.done); _ = c.conn.Close() }) }

type voiceBridge struct {
	gw          *Gateway
	conn        *net.UDPConn
	destination *net.UDPAddr
	mu          sync.Mutex
	clients     map[*voiceClient]bool
	talker      *voiceClient
	talkStarted time.Time
	lastAudio   time.Time
	sequence    uint32
}

func (b *voiceBridge) status() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	talker := ""
	if b.talker != nil {
		talker = b.talker.session.Callsign
	}
	return map[string]any{"ready": true, "clients": len(b.clients), "transmitting": b.talker != nil, "talker": talker, "node": 1997, "conference_node": 530471}
}

func newVoiceBridge(gw *Gateway, listen, destination string) (*voiceBridge, error) {
	listenAddr, err := net.ResolveUDPAddr("udp4", listen)
	if err != nil {
		return nil, err
	}
	destAddr, err := net.ResolveUDPAddr("udp4", destination)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", listenAddr)
	if err != nil {
		return nil, err
	}
	b := &voiceBridge{gw: gw, conn: conn, destination: destAddr, clients: make(map[*voiceClient]bool)}
	go b.readLoop()
	go b.watchdog()
	return b, nil
}

func (b *voiceBridge) serve(w http.ResponseWriter, r *http.Request, session clientSession) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &voiceClient{conn: conn, session: session, send: make(chan WSMessage, 80), done: make(chan struct{})}
	b.mu.Lock()
	b.clients[client] = true
	b.mu.Unlock()
	go func() {
		defer client.close()
		for {
			select {
			case <-client.done:
				return
			case msg := <-client.send:
				_ = client.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if client.conn.WriteMessage(msg.messageType, msg.data) != nil {
					return
				}
			}
		}
	}()
	b.sendStatus(client, "connected", "")
	defer func() {
		b.stop(client, "client disconnected")
		b.mu.Lock()
		delete(b.clients, client)
		b.mu.Unlock()
		client.close()
	}()
	conn.SetReadLimit(2048)
	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch messageType {
		case websocket.TextMessage:
			var request struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(payload, &request) != nil {
				continue
			}
			switch request.Type {
			case "ptt_start":
				b.start(client)
			case "ptt_stop":
				b.stop(client, "released")
			}
		case websocket.BinaryMessage:
			if len(payload) != 320 {
				b.sendStatus(client, "denied", "Audio frames must be 160 PCM16 samples")
				continue
			}
			b.audio(client, payload)
		}
	}
}

func (b *voiceBridge) sendStatus(client *voiceClient, state, reason string) {
	payload, _ := json.Marshal(map[string]any{"type": "voice_status", "state": state, "reason": reason, "callsign": client.session.Callsign, "dmr_id": client.session.DMRID})
	select {
	case client.send <- WSMessage{websocket.TextMessage, payload}:
	default:
	}
}

func (b *voiceBridge) start(client *voiceClient) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.talker != nil && b.talker != client {
		b.sendStatus(client, "busy", "Another station is transmitting")
		return
	}
	if b.talker == client {
		return
	}
	b.talker = client
	b.talkStarted = time.Now()
	b.lastAudio = time.Now()
	b.sequence++
	_, _ = b.conn.WriteToUDP(buildUSRPIdentity(b.sequence, client.session), b.destination)
	b.sendStatus(client, "transmitting", "")
}

func (b *voiceBridge) audio(client *voiceClient, pcm []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.talker != client {
		b.sendStatus(client, "denied", "Press PTT before sending audio")
		return
	}
	if time.Since(b.talkStarted) > clientVoiceMaxCall {
		b.stopLocked(client, "maximum call time reached")
		return
	}
	b.lastAudio = time.Now()
	b.sequence++
	_, _ = b.conn.WriteToUDP(buildUSRPVoice(b.sequence, pcm), b.destination)
}

func (b *voiceBridge) stop(client *voiceClient, reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopLocked(client, reason)
}
func (b *voiceBridge) stopLocked(client *voiceClient, reason string) {
	if b.talker != client {
		return
	}
	b.sequence++
	_, _ = b.conn.WriteToUDP(buildUSRPEOT(b.sequence), b.destination)
	b.talker = nil
	b.sendStatus(client, "receiving", reason)
}

func (b *voiceBridge) watchdog() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		b.mu.Lock()
		if b.talker != nil && (time.Since(b.lastAudio) > 2*time.Second || time.Since(b.talkStarted) > clientVoiceMaxCall) {
			b.stopLocked(b.talker, "transmit watchdog released PTT")
		}
		b.mu.Unlock()
	}
}

func (b *voiceBridge) readLoop() {
	buffer := make([]byte, 512)
	for {
		n, source, err := b.conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if source.IP.String() != "127.0.0.1" || source.Port != 32018 {
			continue
		}
		if n != 352 || string(buffer[:4]) != "USRP" || buffer[15] != 1 || buffer[20] != 0 {
			continue
		}
		pcm := append([]byte(nil), buffer[32:352]...)
		b.mu.Lock()
		for client := range b.clients {
			if client != b.talker {
				select {
				case client.send <- WSMessage{websocket.BinaryMessage, pcm}:
				default:
				}
			}
		}
		b.mu.Unlock()
	}
}

func buildUSRPBase(sequence uint32, size int) []byte {
	packet := make([]byte, size)
	copy(packet, "USRP")
	binary.BigEndian.PutUint32(packet[4:8], sequence)
	return packet
}
func buildUSRPVoice(sequence uint32, pcm []byte) []byte {
	packet := buildUSRPBase(sequence, 352)
	packet[15] = 1
	packet[20] = 0
	copy(packet[32:], pcm)
	return packet
}
func buildUSRPEOT(sequence uint32) []byte { return buildUSRPBase(sequence, 32) }
func buildUSRPIdentity(sequence uint32, session clientSession) []byte {
	packet := buildUSRPBase(sequence, 352)
	packet[15] = 1
	packet[20] = 2
	packet[32] = 8
	callsign := []byte(session.Callsign)
	if len(callsign) > 16 {
		callsign = callsign[:16]
	}
	packet[33] = byte(14 + len(callsign))
	packet[34] = byte(session.DMRID >> 16)
	packet[35] = byte(session.DMRID >> 8)
	packet[36] = byte(session.DMRID)
	targetID := uint32(23530)
	packet[41] = byte(targetID >> 16)
	packet[42] = byte(targetID >> 8)
	packet[43] = byte(targetID)
	packet[44] = 2
	packet[45] = 1
	copy(packet[46:], callsign)
	return packet
}
