package main

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketRequiresOneTimeTicket(t *testing.T) {
	gateway := &Gateway{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ws", nil)
	gateway.handleWS(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated websocket returned %d, want 401", recorder.Code)
	}
}

func TestWebSocketTicketSupportsPublicReadOnlyAndIsSingleUse(t *testing.T) {
	gateway := &Gateway{wsTickets: make(map[string]wsTicket)}

	unauthenticated := httptest.NewRecorder()
	gateway.handleWSTicket(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/ws_ticket", nil))
	if unauthenticated.Code != http.StatusOK {
		t.Fatalf("public ticket request returned %d, want 200", unauthenticated.Code)
	}
	var publicPayload struct {
		Ticket        string `json:"ticket"`
		Authenticated bool   `json:"authenticated"`
	}
	if err := json.Unmarshal(unauthenticated.Body.Bytes(), &publicPayload); err != nil {
		t.Fatal(err)
	}
	if publicPayload.Authenticated || len(publicPayload.Ticket) != 64 {
		t.Fatalf("public ticket payload was unexpected: %+v", publicPayload)
	}
	if user, ok := gateway.consumeWSTicket(publicPayload.Ticket); !ok || user != "" {
		t.Fatalf("public ticket resolved to user %q with valid=%v", user, ok)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/ws_ticket", nil)
	request.Header.Set("X-DVHub-Authenticated-User", "2e0lxy")
	recorder := httptest.NewRecorder()
	gateway.handleWSTicket(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated ticket request returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Ticket) != 64 {
		t.Fatalf("ticket length is %d, want 64", len(payload.Ticket))
	}
	if user, ok := gateway.consumeWSTicket(payload.Ticket); !ok || user != "2E0LXY" {
		t.Fatalf("ticket resolved to %q with valid=%v, want 2E0LXY", user, ok)
	}
	if user, ok := gateway.consumeWSTicket(payload.Ticket); ok {
		t.Fatalf("ticket was reusable by %q", user)
	}
}

func TestWebSocketDisconnectDoesNotRequireConnectionFields(t *testing.T) {
	gateway := &Gateway{clients: make(map[*WSClient]bool), wsTickets: make(map[string]wsTicket)}
	for index := range gateway.sessions {
		gateway.sessions[index] = &UserSession{ID: index + 1}
	}
	session := gateway.sessions[2]
	session.LinkActive = true
	session.Mode = "DMR"
	session.Target = "BrandMeister-UK-2341"

	ticket, err := gateway.issueWSTicket("2E0LXY")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(gateway.handleWS))
	defer server.Close()
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?ticket="+ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.WriteJSON(map[string]any{"cmd": "node_state", "node_id": 3, "active": false}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session.mu.RLock()
		active := session.LinkActive
		session.mu.RUnlock()
		if !active {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("manual session remained active after a field-minimal disconnect command")
}

func TestControlRequiresAuthenticationAndCSRFHeader(t *testing.T) {
	for _, test := range []struct {
		user, control string
		want          int
	}{{"", "1", http.StatusUnauthorized}, {"2E0LXY", "", http.StatusForbidden}} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/control", nil)
		request.Header.Set("X-DVHub-Authenticated-User", test.user)
		request.Header.Set("X-DVHub-Control", test.control)
		if requireControlAuth(recorder, request) {
			t.Fatal("invalid control request was accepted")
		}
		if recorder.Code != test.want {
			t.Fatalf("control auth returned %d, want %d", recorder.Code, test.want)
		}
	}
}

func TestPublicDashboardIsReadOnlyAndDoesNotExposeSecrets(t *testing.T) {
	gateway := &Gateway{}
	for index := range gateway.sessions {
		gateway.sessions[index] = &UserSession{ID: index + 1}
	}
	recorder := httptest.NewRecorder()
	gateway.handlePublicDashboard(recorder, httptest.NewRequest(http.MethodGet, "/api/public/dashboard", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("public dashboard returned %d: %s", recorder.Code, recorder.Body.String())
	}
	body := strings.ToLower(recorder.Body.String())
	for _, forbidden := range []string{"password", "email", "address", "contact", "secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("public dashboard exposed forbidden field %q", forbidden)
		}
	}

	postRecorder := httptest.NewRecorder()
	gateway.handlePublicDashboard(postRecorder, httptest.NewRequest(http.MethodPost, "/api/public/dashboard", strings.NewReader("{}")))
	if postRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("public dashboard accepted POST with status %d", postRecorder.Code)
	}
}

func TestEchoLinkConfigurationIsBoundToAuthenticatedCallsign(t *testing.T) {
	gateway := &Gateway{}
	tests := []struct {
		name     string
		user     string
		control  string
		body     string
		wantCode int
	}{
		{
			name:     "unauthenticated",
			control:  "1",
			body:     `{"callsign":"2E0LXY-L","node":123456,"password":"validpass","email":"radio@example.test","name":"Daz","qth":"Yorkshire"}`,
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "missing csrf guard",
			user:     "2E0LXY",
			body:     `{"callsign":"2E0LXY-L","node":123456,"password":"validpass","email":"radio@example.test","name":"Daz","qth":"Yorkshire"}`,
			wantCode: http.StatusForbidden,
		},
		{
			name:     "different operator",
			user:     "2E0LXY",
			control:  "1",
			body:     `{"callsign":"M0ABC-L","node":123456,"password":"validpass","email":"radio@example.test","name":"Daz","qth":"Yorkshire"}`,
			wantCode: http.StatusForbidden,
		},
		{
			name:     "invalid node",
			user:     "2E0LXY",
			control:  "1",
			body:     `{"callsign":"2E0LXY-L","node":0,"password":"validpass","email":"radio@example.test","name":"Daz","qth":"Yorkshire"}`,
			wantCode: http.StatusBadRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/echolink_config", strings.NewReader(test.body))
			request.Header.Set("X-DVHub-Authenticated-User", test.user)
			request.Header.Set("X-DVHub-Control", test.control)
			gateway.handleEchoLinkConfig(recorder, request)
			if recorder.Code != test.wantCode {
				t.Fatalf("EchoLink configuration returned %d, want %d: %s", recorder.Code, test.wantCode, recorder.Body.String())
			}
		})
	}
}

func TestStateDoesNotExposeResolvedHomeAddress(t *testing.T) {
	gateway := &Gateway{}
	for index := range gateway.sessions {
		gateway.sessions[index] = &UserSession{ID: index + 1}
	}
	recorder := httptest.NewRecorder()
	gateway.handleState(recorder, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, leaked := payload["ip"]; leaked {
		t.Fatal("state response still exposes the home IP field")
	}
}

func TestSystemStatsIncludesOperationalHealthFields(t *testing.T) {
	recorder := httptest.NewRecorder()
	handleSystemStats(recorder, httptest.NewRequest(http.MethodGet, "/api/system", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("system status returned %d", recorder.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"gateway_service", "caddy_service", "gitops_timer", "gitops_revision", "public_url",
		"timezone", "timezone_correct", "swap_active", "config_access",
		"vocoder_config", "ysf_reflector", "ysf_bridge", "p25_reflector",
		"p25_bridge", "nxdn_reflector", "nxdn_bridge", "local_master",
		"m17_bridge", "xlxd_reflector", "dvxcode_bridge", "allstar_bridge", "allstar_services", "echolink_ready", "quality",
	} {
		if _, ok := payload[field]; !ok {
			t.Errorf("system status is missing %q", field)
		}
	}
}

func TestModeControlRequiresAuthenticationAndRejectsUnknownTargets(t *testing.T) {
	gateway := &Gateway{}
	tests := []struct {
		name     string
		user     string
		control  string
		body     string
		wantCode int
	}{
		{"unauthenticated", "", "1", `{"mode":"ysf","component":"reflector","action":"restart"}`, http.StatusUnauthorized},
		{"missing csrf", "2E0LXY", "", `{"mode":"ysf","component":"reflector","action":"restart"}`, http.StatusForbidden},
		{"unknown mode", "2E0LXY", "1", `{"mode":"zello","component":"reflector","action":"start"}`, http.StatusBadRequest},
		{"unknown component", "2E0LXY", "1", `{"mode":"ysf","component":"database","action":"start"}`, http.StatusBadRequest},
		{"unknown action", "2E0LXY", "1", `{"mode":"ysf","component":"reflector","action":"enable"}`, http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/mode_control", strings.NewReader(test.body))
			request.Header.Set("X-DVHub-Authenticated-User", test.user)
			request.Header.Set("X-DVHub-Control", test.control)
			gateway.handleModeControl(recorder, request)
			if recorder.Code != test.wantCode {
				t.Fatalf("mode control returned %d, want %d: %s", recorder.Code, test.wantCode, recorder.Body.String())
			}
		})
	}
}

func TestPassiveNetworkQualityReportsDeltaWithoutGeneratingTraffic(t *testing.T) {
	quality := passiveNetworkQuality{}
	now := time.Now()
	quality.observe(networkCounterSample{Interface: "eth0", Packets: 1000, Dropped: 10, At: now})
	quality.observe(networkCounterSample{Interface: "eth0", Packets: 1999, Dropped: 11, At: now.Add(5 * time.Second)})
	snapshot := quality.snapshot()
	if snapshot["received_packets"] != uint64(999) || snapshot["dropped_packets"] != uint64(1) {
		t.Fatalf("unexpected passive network delta: %#v", snapshot)
	}
	if snapshot["level"] != "warning" {
		t.Fatalf("network quality level = %v, want warning", snapshot["level"])
	}
}

func TestPassiveDMRQualityDetectsSequenceGap(t *testing.T) {
	quality := passiveDMRQuality{flows: make(map[string]dmrFlowQuality)}
	quality.observe("test", DMRTrafficMeta{StreamID: 42, RepeaterID: 7, Sequence: 10})
	quality.observe("test", DMRTrafficMeta{StreamID: 42, RepeaterID: 7, Sequence: 12})
	snapshot := quality.snapshot()
	if snapshot["missing_packets"] != uint64(1) {
		t.Fatalf("missing packets = %v, want 1", snapshot["missing_packets"])
	}
	if snapshot["level"] != "fault" {
		t.Fatalf("DMR quality level = %v, want fault", snapshot["level"])
	}
}

func TestPassiveVocoderQualityFlagsDeadlineMiss(t *testing.T) {
	quality := passiveVocoderQuality{}
	quality.observe(12*time.Millisecond, true)
	quality.observe(40*time.Millisecond, false)
	snapshot := quality.snapshot()
	if snapshot["failures"] != uint64(1) {
		t.Fatalf("vocoder failures = %v, want 1", snapshot["failures"])
	}
	if snapshot["level"] != "fault" {
		t.Fatalf("vocoder quality level = %v, want fault", snapshot["level"])
	}
}

func TestGracefulShutdownLogsOutRunningDMRSessions(t *testing.T) {
	master, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	gateway := &Gateway{}
	gateway.sessions[0] = &UserSession{
		ID: 1, Mode: "DMR", LinkActive: true, AuthStage: authRunning,
		RepeaterID: 234439901, Conn: client, RemoteAddr: master.LocalAddr().(*net.UDPAddr),
	}
	gateway.logoutNetworkSessions()
	if err := master.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 32)
	n, _, err := master.ReadFromUDP(packet)
	if err != nil {
		t.Fatal(err)
	}
	if n != 9 || string(packet[:5]) != "RPTCL" || binary.BigEndian.Uint32(packet[5:9]) != 234439901 {
		t.Fatalf("unexpected logout packet: %x", packet[:n])
	}
}

func TestTimezoneFromLink(t *testing.T) {
	for link, want := range map[string]string{
		"/usr/share/zoneinfo/Europe/London":   "Europe/London",
		"../usr/share/zoneinfo/Europe/London": "Europe/London",
		"/unexpected/timezone":                "",
	} {
		if got := timezoneFromLink(link); got != want {
			t.Errorf("timezoneFromLink(%q) = %q, want %q", link, got, want)
		}
	}
}

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

func TestDV30NetworkEncodeAndDecode(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	server.SetDeadline(time.Now().Add(2 * time.Second))
	done := make(chan error, 1)
	go func() {
		buffer := make([]byte, 512)
		for requestNumber := 0; requestNumber < 2; requestNumber++ {
			n, client, readErr := server.ReadFromUDP(buffer)
			if readErr != nil {
				done <- readErr
				return
			}
			switch buffer[0] {
			case 0x61:
				if n != 322 {
					done <- &net.AddrError{Err: "invalid encode request", Addr: client.String()}
					return
				}
				_, readErr = server.WriteToUDP(append([]byte{0x62, buffer[1]}, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}...), client)
			case 0x63:
				if n != 11 {
					done <- &net.AddrError{Err: "invalid decode request", Addr: client.String()}
					return
				}
				_, readErr = server.WriteToUDP(append([]byte{0x64, buffer[1]}, make([]byte, 320)...), client)
			default:
				done <- &net.AddrError{Err: "unknown opcode", Addr: client.String()}
				return
			}
			if readErr != nil {
				done <- readErr
				return
			}
		}
		done <- nil
	}()

	target := server.LocalAddr().(*net.UDPAddr)
	encoded := encodeDV30(make([]byte, 320), target, 1)
	if string(encoded) != string([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9}) {
		t.Fatalf("unexpected hardware encode result: %x", encoded)
	}
	decoded := decodeDV30(encoded, target, 1)
	if len(decoded) != 320 {
		t.Fatalf("hardware decode returned %d bytes, want 320", len(decoded))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
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

func TestBridgeTalkerTelemetryReportsCurrentFirstTalker(t *testing.T) {
	gateway := &Gateway{}
	gateway.sessions[0] = &UserSession{ID: 1, Target: "FreeSTAR-SystemX-UK", TG: 23530}
	route := BridgeRoute{
		Active: true, ActiveNode: 1, ActiveStream: 12345,
		ActiveUntil: time.Now().Add(time.Second),
	}
	talker := gateway.bridgeTalker(route)
	if talker["busy"] != true || talker["network"] != "FreeSTAR-SystemX-UK" || talker["talkgroup"] != uint32(23530) || talker["stream_id"] != uint32(12345) {
		t.Fatalf("unexpected first-talker telemetry: %#v", talker)
	}
	route.ActiveUntil = time.Now().Add(-time.Second)
	if expired := gateway.bridgeTalker(route); expired["busy"] != false || expired["hold_milliseconds"] != int64(0) {
		t.Fatalf("expired first-talker telemetry remained busy: %#v", expired)
	}
}

func TestConferenceRepeaterIDUsesESSIDWhereRequired(t *testing.T) {
	const baseID uint32 = 2344399
	if got := conferenceRepeaterID("BrandMeister-UK-2341", baseID, 1); got != 234439901 {
		t.Fatalf("BrandMeister repeater ID = %d, want 234439901", got)
	}
	if got := conferenceRepeaterID("FreeSTAR-SystemX-UK", baseID, 1); got != 234439901 {
		t.Fatalf("FreeSTAR repeater ID = %d, want 234439901", got)
	}
	if got := conferenceRepeaterID("TGIF", baseID, 1); got != baseID {
		t.Fatalf("TGIF repeater ID = %d, want %d", got, baseID)
	}
}

func TestParseDV30AddressAcceptsHostnameURLAndRejectsEmpty(t *testing.T) {
	if parseDV30Address("") != nil {
		t.Fatal("empty DV30 address was accepted")
	}
	for _, value := range []string{"localhost:2468", "https://localhost"} {
		addr := parseDV30Address(value)
		if addr == nil || addr.Port != 2468 {
			t.Fatalf("DV30 address %q parsed as %#v", value, addr)
		}
	}
}

func TestSessionVocoderTargetsUsesConfiguredSecondDevice(t *testing.T) {
	session := &UserSession{}
	primary := parseDV30Address("127.0.0.1:2468")
	secondary := parseDV30Address("127.0.0.1:2469")
	session.DV30Addr.Store(primary)
	session.DV30Addr2.Store(secondary)
	session.DV30Count.Store(2)
	targets := sessionVocoderTargets(session)
	if len(targets) != 2 || targets[0].String() != primary.String() || targets[1].String() != secondary.String() {
		t.Fatalf("unexpected two-device target pool: %#v", targets)
	}
	session.DV30Count.Store(1)
	if targets = sessionVocoderTargets(session); len(targets) != 1 || targets[0].String() != primary.String() {
		t.Fatalf("single-device target pool = %#v", targets)
	}
}

func TestLocalVocoderBrokerFailsClosedWithoutHardware(t *testing.T) {
	session := &UserSession{}
	session.DV30Addr.Store((*net.UDPAddr)(nil))
	session.DV30Addr2.Store((*net.UDPAddr)(nil))
	session.DV30Count.Store(1)
	gateway := &Gateway{}
	gateway.sessions[0] = session

	encodeRequest := append([]byte{0x61, 0x42}, make([]byte, 320)...)
	if response := gateway.handleLocalVocoderRequest(encodeRequest); len(response) != 2 || response[0] != 0x7f || response[1] != 0x42 {
		t.Fatalf("invalid broker encode response: %x", response)
	}
	decodeRequest := append([]byte{0x63, 0x43}, make([]byte, 9)...)
	if response := gateway.handleLocalVocoderRequest(decodeRequest); len(response) != 2 || response[0] != 0x7f || response[1] != 0x43 {
		t.Fatalf("invalid broker decode response: length=%d response=%x", len(response), response)
	}
	if response := gateway.handleLocalVocoderRequest([]byte{0x70}); len(response) < 2 || response[0] != 0x71 {
		t.Fatalf("invalid broker health response: %x", response)
	}
	if response := gateway.handleLocalVocoderRequest(nil); len(response) != 2 || response[0] != 0x7f {
		t.Fatalf("invalid broker error response: %x", response)
	}
}

func TestConfiguredDMRHostsCanBeLoaded(t *testing.T) {
	if _, err := os.Stat(dmrHostsPath); os.IsNotExist(err) {
		t.Skip("live DMR_Hosts.txt is not installed in this test environment")
	}
	wantsUserCredential := map[string]bool{
		"FreeSTAR-SystemX-UK":  false,
		"BrandMeister-UK-2341": true,
		"DMRPlus-FreeSTAR":     true,
		"TGIF":                 true,
		"FreeDMR-UK":           false,
	}
	for target, wantUserCredential := range wantsUserCredential {
		entry, err := loadDMRHost(target)
		if err != nil {
			t.Errorf("%s: %v", target, err)
			continue
		}
		if entry.Password == "" || entry.Host == "" || entry.Port == 0 {
			t.Errorf("%s returned an incomplete host entry", target)
		}
		if got := dmrRequiresUserPassword(target, entry); got != wantUserCredential {
			t.Errorf("%s requires user credential = %v, want %v", target, got, wantUserCredential)
		}
	}
}

func TestRegisteredIdentityMustMatchRadioID(t *testing.T) {
	gateway := &Gateway{idDB: map[uint32]RadioIDInfo{2344399: {Callsign: "2E0LXY"}}}
	client := &WSClient{user: "2E0LXY"}
	session := &UserSession{Mode: "DMR", LinkActive: true, AuthStage: authRunning, DMRID: 2344399, Callsign: "2e0lxy"}
	if _, ok, reason := gateway.registeredIdentity(client, session); !ok {
		t.Fatalf("registered matching identity rejected: %s", reason)
	}
	session.Callsign = "M0ABC"
	if _, ok, _ := gateway.registeredIdentity(client, session); ok {
		t.Fatal("mismatched callsign was allowed to transmit")
	}
	session.Callsign, session.DMRID = "2E0LXY", 2000000
	if _, ok, _ := gateway.registeredIdentity(client, session); ok {
		t.Fatal("unregistered DMR ID was allowed to transmit")
	}
}

func TestRegisteredIdentityIsBoundToAuthenticatedAccount(t *testing.T) {
	gateway := &Gateway{idDB: map[uint32]RadioIDInfo{2344399: {Callsign: "2E0LXY"}}}
	session := &UserSession{Mode: "DMR", LinkActive: true, AuthStage: authRunning, DMRID: 2344399, Callsign: "2E0LXY"}
	if _, ok, _ := gateway.registeredIdentity(&WSClient{user: "M0ABC"}, session); ok {
		t.Fatal("another authenticated account was allowed to claim 2E0LXY")
	}
}

func TestJSONUint32RejectsWrapAndFractions(t *testing.T) {
	for _, value := range []any{-1.0, 0.0, 1.5, float64(0x1000000)} {
		if _, ok := jsonUint32(value, 0xFFFFFF); ok {
			t.Fatalf("unsafe value %v was accepted", value)
		}
	}
	if got, ok := jsonUint32(23530.0, 0xFFFFFF); !ok || got != 23530 {
		t.Fatalf("valid talkgroup rejected: %d, %v", got, ok)
	}
}

func TestHardwareVocoderOutageFailsClosed(t *testing.T) {
	request := append([]byte{0x61, 0x01}, make([]byte, 320)...)
	gateway := &Gateway{}
	gateway.sessions[0] = &UserSession{}
	gateway.sessions[0].DV30Addr.Store((*net.UDPAddr)(nil))
	gateway.sessions[0].DV30Addr2.Store((*net.UDPAddr)(nil))
	if response := gateway.handleLocalVocoderRequest(request); len(response) != 2 || response[0] != 0x7f {
		t.Fatalf("hardware outage did not fail closed: %x", response)
	}
}

func TestVocoderTargetConcurrencyIsBounded(t *testing.T) {
	target := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 49991}
	releases := make([]func(), 0, 4)
	for i := 0; i < 4; i++ {
		release, ok := acquireVocoderTarget(target)
		if !ok {
			t.Fatalf("hardware slot %d was unexpectedly unavailable", i+1)
		}
		releases = append(releases, release)
	}
	if release, ok := acquireVocoderTarget(target); ok {
		release()
		t.Fatal("fifth concurrent request exceeded the endpoint bound")
	}
	for _, release := range releases {
		release()
	}
}

func TestVocoderFailoverSharesOneFrameDeadline(t *testing.T) {
	listeners := make([]*net.UDPConn, 0, 2)
	targets := make([]*net.UDPAddr, 0, 2)
	for i := 0; i < 2; i++ {
		listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		targets = append(targets, listener.LocalAddr().(*net.UDPAddr))
		go func(conn *net.UDPConn) {
			buffer := make([]byte, 512)
			_, _, _ = conn.ReadFromUDP(buffer)
		}(listener)
	}
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()

	started := time.Now()
	if encoded := encodeVocoderFrame(make([]byte, 320), targets); encoded != nil {
		t.Fatal("silent hardware endpoints unexpectedly returned audio")
	}
	if elapsed := time.Since(started); elapsed > 70*time.Millisecond {
		t.Fatalf("hardware failover exceeded the frame deadline: %v", elapsed)
	}
}

func TestYSFGatewayEndpointAndIdentityEnrichment(t *testing.T) {
	host, port := splitYSFEndpoint("192.0.2.25:42000")
	if host != "192.0.2.25" || port != 42000 {
		t.Fatalf("endpoint parsed as %q:%d", host, port)
	}
	gateway := &Gateway{idDB: map[uint32]RadioIDInfo{
		2344399: {Callsign: "2E0LXY", Name: "Daren", City: "York", Country: "United Kingdom"},
		2344999: {Callsign: "2E0LXY", Name: "Alternate", City: "Leeds", Country: "United Kingdom"},
		2344000: {Callsign: "M0ABC", Name: "Other"},
	}}
	identities := gateway.ysfGatewayIdentities("2e0lxy")
	if len(identities) != 2 || identities[0].DMRID != 2344399 || identities[1].DMRID != 2344999 {
		t.Fatalf("unexpected YSF identities: %#v", identities)
	}
}

func TestTrafficBroadcastThrottle(t *testing.T) {
	gateway := &Gateway{}
	started := time.Unix(1_700_000_000, 0)
	if !gateway.allowTrafficBroadcast("1/TGIF/2351633/42", started) {
		t.Fatal("first frame in a stream was throttled")
	}
	if gateway.allowTrafficBroadcast("1/TGIF/2351633/42", started.Add(999*time.Millisecond)) {
		t.Fatal("repeat frame inside the one-second window was broadcast")
	}
	if !gateway.allowTrafficBroadcast("1/TGIF/2351633/42", started.Add(time.Second)) {
		t.Fatal("periodic stream update was not released")
	}
	if !gateway.allowTrafficBroadcast("1/TGIF/2351633/43", started.Add(time.Millisecond)) {
		t.Fatal("a new stream was incorrectly throttled")
	}
}

func TestDMRSessionStatePersistsRejection(t *testing.T) {
	now := time.Now()
	session := &UserSession{Mode: "DMR", AuthStage: authDisconnected, LastRejected: now.Add(-time.Minute)}
	if got := dmrSessionState(session); got != "rejected" {
		t.Fatalf("dmrSessionState() = %q, want rejected", got)
	}
	session.LastRejected = now.Add(-6 * time.Minute)
	if got := dmrSessionState(session); got != "rejected" {
		t.Fatalf("persisted rejection state = %q, want rejected", got)
	}
}

func TestParseSystemdUnitStates(t *testing.T) {
	requested := []string{"running.service", "failed.service", "missing.service"}
	states := parseSystemdUnitStates([]byte("Id=running.service\nLoadState=loaded\nActiveState=active\n\nId=failed.service\nLoadState=loaded\nActiveState=failed\n"), requested)
	if !systemdUnitActive(states["running.service"]) || systemdUnitDetail(states["running.service"]) != "Running" {
		t.Fatalf("running service parsed incorrectly: %+v", states["running.service"])
	}
	if systemdUnitActive(states["failed.service"]) || systemdUnitDetail(states["failed.service"]) != "Failed" {
		t.Fatalf("failed service parsed incorrectly: %+v", states["failed.service"])
	}
	if systemdUnitActive(states["missing.service"]) || systemdUnitDetail(states["missing.service"]) != "Not installed" {
		t.Fatalf("missing service parsed incorrectly: %+v", states["missing.service"])
	}
}
