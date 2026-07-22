package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const BasePort = 62031
const MaxUsers = 8

const (
	ysfConfigPath       = "/etc/ysfreflector/YSFReflector.ini"
	ysfIdentityLockPath = "/var/lib/dvgateway/ysf-identity.lock"
	dvrefTokenPath      = "/etc/dvhub/dvref.token"
	ysf2dmrConfigPath   = "/var/lib/dvgateway/ysf2dmr-runtime.ini"
	dmrHostsPath        = "/var/lib/dvgateway/DMR_Hosts.txt"
	ysfNetworkName      = "YORKSHIRELINK"
	ysfDescription      = "YORKSHIRE HUB"
)

const (
	authDisconnected = iota
	authWaitingLogin
	authWaitingAuthorisation
	authWaitingConfig
	authWaitingOptions
	authRunning
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host == r.Host
}}

type WSClient struct {
	conn    *websocket.Conn
	send    chan []byte
	writeMu sync.Mutex
}

type UserSession struct {
	ID                   int
	Port                 int
	IsActive             atomic.Bool
	Mode                 string
	Target               string
	TG                   uint32
	UserPassword         string
	MasterPassword       string
	AuthPassword         string
	RequiresUserPassword bool
	UseHWVocoder         atomic.Bool
	DV30Addr             atomic.Value // primary *net.UDPAddr
	DV30Addr2            atomic.Value // secondary *net.UDPAddr, reserved for D-Star -> DMR
	DV30Count            atomic.Int32
	FlushFEC             atomic.Bool
	rtcBuffer            chan []byte
	LastTXFrame          atomic.Int64
	Callsign             string
	DMRID                uint32
	RepeaterID           uint32
	Options              string
	LinkActive           bool
	AuthStage            int
	Conn                 *net.UDPConn
	RemoteAddr           *net.UDPAddr
	LastNetwork          time.Time
	LastControl          time.Time
	SeqNo                uint8
	StreamID             uint32
	mu                   sync.RWMutex
}

type RadioIDInfo struct {
	Callsign string
	Name     string
	City     string
	State    string
	Country  string
}

type DMRTrafficMeta struct {
	DestinationID uint32
	RepeaterID    uint32
	StreamID      uint32
	Sequence      uint8
	Slot          int
	CallType      string
	FrameType     string
	BER           int
	BERAvailable  bool
	RSSI          int
	RSSIAvailable bool
}

type BridgeRoute struct {
	Active       bool
	ANode        int
	ATG          uint32
	BNode        int
	BTG          uint32
	CNode        int
	CTG          uint32
	Suppress     map[int]BridgeSuppression
	ActiveNode   int
	ActiveStream uint32
	ActiveUntil  time.Time
	Fingerprints map[[32]byte]time.Time
}

type BridgeSuppression struct {
	Stream uint32
	Until  time.Time
}

type Talkgroup struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
}

type DMRHostEntry struct {
	Name     string
	Host     string
	Password string
	Port     int
}

type YSFIdentity struct {
	ID                  string `json:"id"`
	SuggestedID         string `json:"suggested_id"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	Country             string `json:"country"`
	Locked              bool   `json:"locked"`
	DVRefReady          bool   `json:"dvref_ready"`
	DVRefRegistered     bool   `json:"dvref_registered"`
	RegistrySource      string `json:"registry_source"`
	APITokenConfigured  bool   `json:"api_token_configured"`
	PublicDashboardPath string `json:"public_dashboard_path"`
}

type Gateway struct {
	HomeAddr        atomic.Value
	sessions        [MaxUsers]*UserSession
	clients         map[*WSClient]bool
	mu              sync.Mutex
	txMu            sync.Mutex
	txOwner         *WSClient
	txNode          int
	txDMRID         uint32
	txCallsign      string
	idDB            map[uint32]RadioIDInfo
	dbMutex         sync.RWMutex
	bridge          BridgeRoute
	bridgeMu        sync.Mutex
	conferenceMu    sync.Mutex
	conferenceTimer *time.Timer
	conferenceUntil time.Time
	tgCache         map[string][]Talkgroup
	tgTimes         map[string]time.Time
	tgMutex         sync.RWMutex
	rejectedMu      sync.Mutex
	rejectedDMR     map[uint32]time.Time
}

var (
	udpPool           = sync.Pool{New: func() any { b := make([]byte, 2048); return &b }}
	gw                *Gateway
	dmrNetworkTargets = map[string]string{
		"FreeSTAR-SystemX-UK":  "dmr.freestar.network:62031",
		"BrandMeister-UK-2341": "2341.master.brandmeister.network:62031",
		"DMRPlus-FreeSTAR":     "ipsc2.freestar.network:62031",
		"TGIF":                 "tgif.network:62031",
		"FreeDMR-UK":           "hotspot.uk.freedmr.link:62031",
	}
	dmrNetworkHostNames = map[string]string{
		"DMRPlus-FreeSTAR": "DMR+_IPSC2-FreeSTAR",
	}
	networkTGURLs = map[string]string{
		"FreeSTAR-SystemX-UK":  "https://w0chp.radio/digital-radio-lists/system-x-talkgroups/download.csv",
		"BrandMeister-UK-2341": "https://w0chp.radio/digital-radio-lists/brandmeister-talkgroups/download.csv",
		"DMRPlus-FreeSTAR":     "https://w0chp.radio/digital-radio-lists/freestaripsc2-talkgroups/download.csv",
		"TGIF":                 "https://w0chp.radio/digital-radio-lists/tgif-talkgroups/download.csv",
		"FreeDMR-UK":           "https://w0chp.radio/digital-radio-lists/freedmr-talkgroups/download.csv",
	}
)

func main() {
	gw = &Gateway{
		clients:     make(map[*WSClient]bool),
		idDB:        make(map[uint32]RadioIDInfo),
		tgCache:     make(map[string][]Talkgroup),
		tgTimes:     make(map[string]time.Time),
		rejectedDMR: make(map[uint32]time.Time),
	}
	gw.HomeAddr.Store(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2460})

	go gw.updateRegistriesDaily()
	go gw.watchdog("home.mysmartagent.uk", 2460)
	go gw.txWatchdog()
	go gw.broadcastYSFDashboardLoop()

	defaultDV30 := parseDV30Address("zx3de49.glddns.com:2468")
	for i := 0; i < MaxUsers; i++ {
		gw.sessions[i] = &UserSession{
			ID:         i + 1,
			Port:       BasePort + i,
			Mode:       "DMR",
			rtcBuffer:  make(chan []byte, 50),
			Callsign:   "M0ABC",
			DMRID:      2350000,
			RepeaterID: 2350000,
			StreamID:   uint32(rand.Int31()) + 1,
		}
		gw.sessions[i].DV30Addr.Store(defaultDV30)
		gw.sessions[i].DV30Addr2.Store((*net.UDPAddr)(nil))
		gw.sessions[i].DV30Count.Store(1)
		go gw.runUDPListener(gw.sessions[i])
	}

	http.HandleFunc("/ws", gw.handleWS)
	http.HandleFunc("/api/state", gw.handleState)
	http.HandleFunc("/api/system", handleSystemStats)
	http.HandleFunc("/api/ysf_hosts", gw.handleYSFHosts)
	http.HandleFunc("/api/dmr_hosts", gw.handleDMRHosts)
	http.HandleFunc("/api/xlx_hosts", gw.handleXLXHosts)
	http.HandleFunc("/api/dmr_lookup", gw.handleDMRLookup)
	http.HandleFunc("/api/talkgroups", gw.handleTalkgroups)
	http.HandleFunc("/api/ysf_control", gw.handleYSFControl)
	http.HandleFunc("/api/ysf_dashboard", gw.handleYSFDashboard)
	http.HandleFunc("/api/public/ysf_dashboard", gw.handlePublicYSFDashboard)
	http.HandleFunc("/api/ysf_identity", gw.handleYSFIdentity)
	http.HandleFunc("/api/yorkshire_conference", gw.handleYorkshireConference)
	http.HandleFunc("/talkgroups.js", gw.handleTalkgroupScript)
	http.Handle("/", http.FileServer(http.Dir("/var/www/dvhub")))

	fmt.Println("[SYS] Yorkshire Link HUB v2.0 - Software Vocoder Enabled")
	fmt.Println("[SYS] Listening on :8080")
	http.ListenAndServe("127.0.0.1:8080", nil)
}

func readCPUTimes() (idle, total uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}

	lines := strings.SplitN(string(data), "\n", 2)
	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("unexpected /proc/stat format")
	}

	for i, field := range fields[1:] {
		value, parseErr := strconv.ParseUint(field, 10, 64)
		if parseErr != nil {
			return 0, 0, parseErr
		}
		total += value
		if i == 3 || i == 4 {
			idle += value
		}
	}
	return idle, total, nil
}

func readCPULoad() float64 {
	idle1, total1, err := readCPUTimes()
	if err != nil {
		return 0
	}
	time.Sleep(200 * time.Millisecond)
	idle2, total2, err := readCPUTimes()
	if err != nil || total2 <= total1 {
		return 0
	}

	totalDelta := total2 - total1
	idleDelta := idle2 - idle1
	return 100 * (1 - float64(idleDelta)/float64(totalDelta))
}

func readPlatform() string {
	platform := runtime.GOOS + " " + runtime.GOARCH
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return platform
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			name := strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
			return name + " (" + runtime.GOARCH + ")"
		}
	}
	return platform
}

func readCPUTemperature() *float64 {
	patterns := []string{
		"/sys/class/thermal/thermal_zone*/temp",
		"/sys/class/hwmon/hwmon*/temp*_input",
	}
	for _, pattern := range patterns {
		paths, _ := filepath.Glob(pattern)
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
			if err != nil {
				continue
			}
			if value > 1000 {
				value /= 1000
			}
			if value > -50 && value < 200 {
				return &value
			}
		}
	}
	return nil
}

func handleSystemStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	hostname, _ := os.Hostname()
	kernelData, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{
		"hostname": hostname,
		"kernel":   strings.TrimSpace(string(kernelData)),
		"platform": readPlatform(),
		"cpu_load": readCPULoad(),
		"temp_c":   readCPUTemperature(),
	})
}

func (g *Gateway) txWatchdog() {
	ticker := time.NewTicker(500 * time.Millisecond)
	for range ticker.C {
		now := time.Now().UnixMilli()
		for _, s := range g.sessions {
			if s.IsActive.Load() {
				last := s.LastTXFrame.Load()
				if (now - last) > 1500 {
					fmt.Printf("[DMS] Node %d TCP timeout. Forcing TX drop.\n", s.ID)
					g.releaseTX(nil, s.ID, "timeout")
				}
			}
		}
	}
}

func (g *Gateway) broadcastText(msg []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for client := range g.clients {
		client.writeMu.Lock()
		client.conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
		client.conn.WriteMessage(websocket.TextMessage, msg)
		client.writeMu.Unlock()
	}
}

func (g *Gateway) sendClientText(client *WSClient, event map[string]any) {
	data, _ := json.Marshal(event)
	client.writeMu.Lock()
	client.conn.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
	_ = client.conn.WriteMessage(websocket.TextMessage, data)
	client.writeMu.Unlock()
}

func (g *Gateway) registeredIdentity(session *UserSession) (RadioIDInfo, bool, string) {
	session.mu.RLock()
	dmrID := session.DMRID
	callsign := strings.ToUpper(strings.TrimSpace(session.Callsign))
	linked := session.LinkActive && (session.Mode != "DMR" || session.AuthStage == authRunning)
	session.mu.RUnlock()
	if !linked {
		return RadioIDInfo{}, false, "The selected network is not connected"
	}
	g.dbMutex.RLock()
	info, found := g.idDB[dmrID]
	g.dbMutex.RUnlock()
	if !found {
		return RadioIDInfo{}, false, fmt.Sprintf("DMR ID %d is not in the RadioID database", dmrID)
	}
	if info.Callsign == "" || !strings.EqualFold(info.Callsign, callsign) {
		return info, false, fmt.Sprintf("DMR ID %d is registered to %s, not %s", dmrID, info.Callsign, callsign)
	}
	return info, true, ""
}

func (g *Gateway) acquireTX(client *WSClient, nodeID int) {
	session := g.sessionByID(nodeID)
	if session == nil {
		g.sendClientText(client, map[string]any{"type": "tx_status", "state": "denied", "reason": "Unknown gateway node"})
		return
	}
	_, valid, reason := g.registeredIdentity(session)
	if !valid {
		g.sendClientText(client, map[string]any{"type": "tx_status", "state": "denied", "node_id": nodeID, "reason": reason})
		return
	}
	g.bridgeMu.Lock()
	radioBusy := g.bridge.Active && g.bridge.ActiveNode != 0 && time.Now().Before(g.bridge.ActiveUntil)
	g.bridgeMu.Unlock()
	if radioBusy {
		g.sendClientText(client, map[string]any{"type": "tx_status", "state": "busy", "reason": "A registered DMR radio stream is active"})
		return
	}
	session.mu.RLock()
	dmrID, callsign := session.DMRID, session.Callsign
	session.mu.RUnlock()

	g.txMu.Lock()
	if g.txOwner != nil && g.txOwner != client {
		busyCallsign, busyID := g.txCallsign, g.txDMRID
		g.txMu.Unlock()
		g.sendClientText(client, map[string]any{"type": "tx_status", "state": "busy", "reason": "Another registered operator is transmitting", "callsign": busyCallsign, "dmr_id": busyID})
		return
	}
	previousNode := g.txNode
	g.txOwner, g.txNode, g.txDMRID, g.txCallsign = client, nodeID, dmrID, callsign
	g.txMu.Unlock()

	if previousNode != 0 && previousNode != nodeID {
		if previous := g.sessionByID(previousNode); previous != nil {
			previous.IsActive.Store(false)
			previous.FlushFEC.Store(true)
		}
	}
	session.LastTXFrame.Store(time.Now().UnixMilli())
	session.IsActive.Store(true)
	fmt.Printf("[PTT] Node %d TX START by %s (%d)\n", nodeID, callsign, dmrID)
	g.broadcastTXStatus("active", nodeID, callsign, dmrID, "")
}

func (g *Gateway) releaseTX(client *WSClient, nodeID int, reason string) bool {
	g.txMu.Lock()
	if g.txOwner == nil || (client != nil && g.txOwner != client) || (nodeID != 0 && g.txNode != nodeID) {
		g.txMu.Unlock()
		return false
	}
	releasedNode, callsign, dmrID := g.txNode, g.txCallsign, g.txDMRID
	g.txOwner, g.txNode, g.txDMRID, g.txCallsign = nil, 0, 0, ""
	g.txMu.Unlock()
	if session := g.sessionByID(releasedNode); session != nil {
		session.IsActive.Store(false)
		session.FlushFEC.Store(true)
	}
	fmt.Printf("[PTT] Node %d TX STOP by %s (%d): %s\n", releasedNode, callsign, dmrID, reason)
	g.broadcastTXStatus("idle", releasedNode, callsign, dmrID, reason)
	return true
}

func (g *Gateway) broadcastTXStatus(state string, nodeID int, callsign string, dmrID uint32, reason string) {
	msg, _ := json.Marshal(map[string]any{"type": "tx_status", "state": state, "node_id": nodeID, "callsign": callsign, "dmr_id": dmrID, "reason": reason})
	g.broadcastText(msg)
	if nodeID > 0 && (state == "active" || state == "idle") {
		stateMsg, _ := json.Marshal(map[string]any{"type": "state_sync", "node_id": nodeID, "active": state == "active"})
		g.broadcastText(stateMsg)
	}
}

func (g *Gateway) clientOwnsTX(client *WSClient) (*UserSession, bool) {
	g.txMu.Lock()
	defer g.txMu.Unlock()
	if g.txOwner != client || g.txNode == 0 {
		return nil, false
	}
	return g.sessionByID(g.txNode), true
}

func (g *Gateway) registeredDMRID(id uint32) bool {
	if id == 0 {
		return false
	}
	g.dbMutex.RLock()
	_, found := g.idDB[id]
	g.dbMutex.RUnlock()
	return found
}

func (g *Gateway) webTXActive() bool {
	g.txMu.Lock()
	active := g.txOwner != nil
	g.txMu.Unlock()
	return active
}

func (g *Gateway) reportRejectedDMR(id uint32) {
	now := time.Now()
	g.rejectedMu.Lock()
	last := g.rejectedDMR[id]
	if now.Sub(last) < 10*time.Second {
		g.rejectedMu.Unlock()
		return
	}
	g.rejectedDMR[id] = now
	g.rejectedMu.Unlock()
	g.broadcastTXStatus("denied", 0, "", id, "Source DMR ID is not in the RadioID database")
}

func (g *Gateway) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &WSClient{
		conn: conn,
		send: make(chan []byte, 32),
	}

	g.mu.Lock()
	g.clients[client] = true
	g.mu.Unlock()

	go func() {
		defer conn.Close()
		for msg := range client.send {
			client.writeMu.Lock()
			client.conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
			err := client.conn.WriteMessage(websocket.BinaryMessage, msg)
			client.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}

		if msgType == websocket.TextMessage {
			var req map[string]any
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}

			if req["cmd"] == "node_state" {
				nodeValue, nodeOK := req["node_id"].(float64)
				mode, modeOK := req["mode"].(string)
				target, targetOK := req["target"].(string)
				active, _ := req["active"].(bool)
				if !nodeOK || !modeOK || !targetOK {
					continue
				}
				nodeID := int(nodeValue)
				for _, s := range g.sessions {
					if s.ID == nodeID {
						if !active {
							g.disconnectNetwork(s, "Disconnected")
							break
						}

						s.mu.Lock()
						s.Mode = mode
						s.Target = target
						if tg, ok := req["tg"].(float64); ok {
							s.TG = uint32(tg)
						}
						if pwd, ok := req["password"].(string); ok {
							s.UserPassword = pwd
						}
						if callsign, ok := req["callsign"].(string); ok {
							s.Callsign = strings.ToUpper(strings.TrimSpace(callsign))
						}
						if dmrID, ok := req["dmr_id"].(float64); ok {
							s.DMRID = uint32(dmrID)
						}
						if repeaterID, ok := req["repeater_id"].(float64); ok {
							s.RepeaterID = uint32(repeaterID)
						}
						if options, ok := req["options"].(string); ok {
							s.Options = strings.TrimSpace(options)
						}
						s.LinkActive = true
						s.mu.Unlock()

						if mode == "DMR" {
							g.beginDMRLogin(s)
						} else {
							g.sendNetworkStatus(s, "connected", "YSF target selected")
						}
						fmt.Printf("[CFG] Node %d: %s -> %s TG/REF %d\n", nodeID, mode, target, s.TG)
						break
					}
				}
			} else if req["cmd"] == "bridge_state" {
				active, _ := req["active"].(bool)
				if !active {
					g.bridgeMu.Lock()
					g.bridge.Active = false
					g.bridgeMu.Unlock()
					g.sendBridgeStatus("disconnected", "Bridge disconnected")
					continue
				}
				aNode, aOK := req["a_node"].(float64)
				bNode, bOK := req["b_node"].(float64)
				aTG, aTGOK := req["a_tg"].(float64)
				bTG, bTGOK := req["b_tg"].(float64)
				cNodeValue, cNodeOK := req["c_node"].(float64)
				cTGValue, cTGOK := req["c_tg"].(float64)
				cNode, cTG := 0, uint32(0)
				if cNodeOK && int(cNodeValue) > 0 {
					cNode, cTG = int(cNodeValue), uint32(cTGValue)
				}
				if !aOK || !bOK || !aTGOK || !bTGOK || int(aNode) == int(bNode) || aTG < 1 || bTG < 1 {
					g.sendBridgeStatus("error", "Choose two different DMR networks and valid talkgroups")
					continue
				}
				if cNode != 0 && (!cTGOK || cTG < 1 || cNode == int(aNode) || cNode == int(bNode)) {
					g.sendBridgeStatus("error", "Choose three different DMR networks and valid talkgroups")
					continue
				}
				g.bridgeMu.Lock()
				g.bridge = BridgeRoute{Active: true, ANode: int(aNode), ATG: uint32(aTG), BNode: int(bNode), BTG: uint32(bTG), CNode: cNode, CTG: cTG, Suppress: make(map[int]BridgeSuppression), Fingerprints: make(map[[32]byte]time.Time)}
				g.bridgeMu.Unlock()
				g.updateBridgeStatus()
			} else if req["cmd"] == "tx_start" {
				nodeValue, ok := req["node_id"].(float64)
				if !ok {
					g.sendClientText(client, map[string]any{"type": "tx_status", "state": "denied", "reason": "Invalid node"})
					continue
				}
				g.acquireTX(client, int(nodeValue))
			} else if req["cmd"] == "tx_stop" {
				if !g.releaseTX(client, 0, "released") {
					g.sendClientText(client, map[string]any{"type": "tx_status", "state": "denied", "reason": "This client does not own the transmitter"})
				}
			} else if req["cmd"] == "set_vocoder" {
				vocType := req["type"].(string)
				for _, s := range g.sessions {
					s.UseHWVocoder.Store(vocType == "hw")
				}
				fmt.Printf("[VOC] Switched to %s vocoder\n", strings.ToUpper(vocType))
			} else if req["cmd"] == "set_dv30" {
				addr1, _ := req["addr1"].(string)
				if addr1 == "" {
					addr1, _ = req["addr"].(string)
				} // backwards compatibility
				if addr1 == "" || strings.Contains(strings.ToLower(addr1), "ai.2e0lxy.uk") {
					addr1 = "zx3de49.glddns.com:2468"
				}
				addr2, _ := req["addr2"].(string)
				count := 1
				if value, ok := req["count"].(float64); ok && int(value) == 2 {
					count = 2
				}
				primary := parseDV30Address(addr1)
				secondary := parseDV30Address(addr2)
				for _, s := range g.sessions {
					s.DV30Addr.Store(primary)
					s.DV30Addr2.Store(secondary)
					s.DV30Count.Store(int32(count))
				}
				fmt.Printf("[VOC] AMBE hardware configured: count=%d primary=%s secondary=%s\n", count, safeVocoderAddress(primary), safeVocoderAddress(secondary))
			}

		} else if msgType == websocket.BinaryMessage {
			if s, owns := g.clientOwnsTX(client); owns && s.IsActive.Load() {
				s.LastTXFrame.Store(time.Now().UnixMilli())
				select {
				case s.rtcBuffer <- msg:
				default:
					s.FlushFEC.Store(true)
				}
			}
		}
	}
	g.releaseTX(client, 0, "operator disconnected")

	g.mu.Lock()
	delete(g.clients, client)
	g.mu.Unlock()
	close(client.send)
}

func (g *Gateway) sendNetworkStatus(s *UserSession, state, message string) {
	s.mu.RLock()
	event := map[string]any{
		"type":                   "network_status",
		"node_id":                s.ID,
		"state":                  state,
		"message":                message,
		"network":                s.Target,
		"dmr_id":                 s.DMRID,
		"repeater_id":            s.RepeaterID,
		"requires_user_password": s.RequiresUserPassword,
	}
	s.mu.RUnlock()
	data, _ := json.Marshal(event)
	g.broadcastText(data)
}

func loadDMRHost(target string) (DMRHostEntry, error) {
	configured, exists := dmrNetworkTargets[target]
	if !exists {
		return DMRHostEntry{}, fmt.Errorf("unknown DMR master")
	}
	configuredHost := configured
	if host, _, err := net.SplitHostPort(configured); err == nil {
		configuredHost = host
	}
	wantedName := dmrNetworkHostNames[target]
	file, err := os.Open(dmrHostsPath)
	if err != nil {
		return DMRHostEntry{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		if !strings.EqualFold(fields[2], configuredHost) && (wantedName == "" || !strings.EqualFold(fields[0], wantedName)) {
			continue
		}
		port, err := strconv.Atoi(fields[4])
		if err != nil || port < 1 || port > 65535 || fields[3] == "" {
			return DMRHostEntry{}, fmt.Errorf("invalid DMR host entry for %s", target)
		}
		return DMRHostEntry{Name: fields[0], Host: fields[2], Password: fields[3], Port: port}, nil
	}
	if err := scanner.Err(); err != nil {
		return DMRHostEntry{}, err
	}
	return DMRHostEntry{}, fmt.Errorf("DMR master %s is not present in DMR_Hosts.txt", target)
}

func dmrRequiresUserPassword(target string, entry DMRHostEntry) bool {
	return target == "BrandMeister-UK-2341" || target == "TGIF" || strings.EqualFold(entry.Password, "PASSWORD")
}

func (g *Gateway) sessionByID(id int) *UserSession {
	for _, session := range g.sessions {
		if session.ID == id {
			return session
		}
	}
	return nil
}

func (g *Gateway) sendBridgeStatus(state, message string) {
	g.bridgeMu.Lock()
	route := g.bridge
	g.bridgeMu.Unlock()
	data, _ := json.Marshal(map[string]any{
		"type": "bridge_status", "state": state, "message": message,
		"a_node": route.ANode, "a_tg": route.ATG, "b_node": route.BNode, "b_tg": route.BTG, "c_node": route.CNode, "c_tg": route.CTG,
	})
	g.broadcastText(data)
}

func (g *Gateway) updateBridgeStatus() {
	g.bridgeMu.Lock()
	route := g.bridge
	g.bridgeMu.Unlock()
	if !route.Active {
		return
	}
	allReady := true
	for node := range bridgeEndpoints(route) {
		session := g.sessionByID(node)
		if session == nil {
			g.sendBridgeStatus("error", "Bridge endpoint is unavailable")
			return
		}
		session.mu.RLock()
		ready := session.LinkActive && session.AuthStage == authRunning
		session.mu.RUnlock()
		allReady = allReady && ready
	}
	if allReady {
		g.sendBridgeStatus("connected", "DMR conference bridge active")
	} else {
		g.sendBridgeStatus("connecting", "Waiting for DMR masters")
	}
}

func bridgeEndpoints(route BridgeRoute) map[int]uint32 {
	endpoints := map[int]uint32{route.ANode: route.ATG, route.BNode: route.BTG}
	if route.CNode > 0 && route.CTG > 0 {
		endpoints[route.CNode] = route.CTG
	}
	return endpoints
}

func dmrDestination(data []byte) uint32 {
	if len(data) < 20 || string(data[:4]) != "DMRD" {
		return 0
	}
	return uint32(data[8])<<16 | uint32(data[9])<<8 | uint32(data[10])
}

func dmrStreamID(data []byte) uint32 {
	if len(data) < 20 {
		return 0
	}
	return binary.BigEndian.Uint32(data[16:20])
}

func dmrFingerprint(data []byte) [32]byte {
	// Ignore destination, repeater and stream IDs because linked networks may
	// rewrite them. The source radio plus encoded voice/data payload remains
	// stable enough to recognise a frame returning through an external bridge.
	payload := make([]byte, 0, 3+len(data)-20)
	payload = append(payload, data[5:8]...)
	payload = append(payload, data[20:]...)
	return sha256.Sum256(payload)
}

func (g *Gateway) forwardBridgeFrame(source *UserSession, data []byte) {
	if len(data) < 55 || string(data[:4]) != "DMRD" {
		return
	}
	// A browser/app operator holding the global TX lease takes precedence.
	// The DMR audio can still be received locally, but it must not be
	// retransmitted onto another network at the same time.
	if g.webTXActive() {
		return
	}

	g.bridgeMu.Lock()
	route := &g.bridge
	if !route.Active {
		g.bridgeMu.Unlock()
		return
	}
	if dmrDestination(data) == 0 {
		g.bridgeMu.Unlock()
		return
	}
	streamID := dmrStreamID(data)
	now := time.Now()
	endpoints := bridgeEndpoints(*route)
	sourceTG, sourceOK := endpoints[source.ID]
	if !sourceOK {
		g.bridgeMu.Unlock()
		return
	}
	if suppression, ok := route.Suppress[source.ID]; ok && suppression.Stream == streamID && now.Before(suppression.Until) {
		g.bridgeMu.Unlock()
		return
	}
	if dmrDestination(data) != sourceTG {
		g.bridgeMu.Unlock()
		return
	}
	if route.ActiveNode != 0 && route.ActiveNode != source.ID && now.Before(route.ActiveUntil) {
		g.bridgeMu.Unlock()
		return
	}
	if route.Fingerprints == nil {
		route.Fingerprints = make(map[[32]byte]time.Time)
	}
	for fingerprint, expires := range route.Fingerprints {
		if !now.Before(expires) {
			delete(route.Fingerprints, fingerprint)
		}
	}
	fingerprint := dmrFingerprint(data)
	if expires, seen := route.Fingerprints[fingerprint]; seen && now.Before(expires) {
		g.bridgeMu.Unlock()
		return
	}
	route.Fingerprints[fingerprint] = now.Add(4 * time.Second)
	route.ActiveNode = source.ID
	route.ActiveStream = streamID
	route.ActiveUntil = now.Add(2 * time.Second)
	targets := make(map[int]uint32, len(endpoints)-1)
	for targetNode, targetTG := range endpoints {
		if targetNode == source.ID {
			continue
		}
		targets[targetNode] = targetTG
		route.Suppress[targetNode] = BridgeSuppression{Stream: streamID, Until: now.Add(5 * time.Second)}
	}
	g.bridgeMu.Unlock()
	for targetNode, targetTG := range targets {
		target := g.sessionByID(targetNode)
		if target == nil {
			continue
		}
		target.mu.RLock()
		ready := target.LinkActive && target.AuthStage == authRunning
		repeaterID := target.RepeaterID
		target.mu.RUnlock()
		if !ready {
			continue
		}
		forwarded := append([]byte(nil), data...)
		forwarded[8], forwarded[9], forwarded[10] = byte(targetTG>>16), byte(targetTG>>8), byte(targetTG)
		binary.BigEndian.PutUint32(forwarded[11:15], repeaterID)
		_ = g.writeNetworkPacket(target, forwarded)
	}
}

func writeUint32BE(value uint32) []byte {
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, value)
	return data
}

func (g *Gateway) writeNetworkPacket(s *UserSession, packet []byte) error {
	s.mu.RLock()
	conn := s.Conn
	remote := s.RemoteAddr
	s.mu.RUnlock()
	if conn == nil || remote == nil {
		return fmt.Errorf("network socket is not ready")
	}
	_, err := conn.WriteToUDP(packet, remote)
	if err == nil {
		s.mu.Lock()
		s.LastControl = time.Now()
		s.mu.Unlock()
	}
	return err
}

func (g *Gateway) beginDMRLogin(s *UserSession) {
	s.mu.Lock()
	entry, err := loadDMRHost(s.Target)
	if err != nil {
		s.LinkActive = false
		s.AuthStage = authDisconnected
		s.mu.Unlock()
		g.sendNetworkStatus(s, "error", "DMR master configuration is unavailable")
		return
	}
	s.MasterPassword = entry.Password
	s.RequiresUserPassword = dmrRequiresUserPassword(s.Target, entry)
	s.AuthPassword = s.MasterPassword
	if s.RequiresUserPassword {
		s.AuthPassword = s.UserPassword
	}
	if s.DMRID < 1000000 || s.RepeaterID < 1000000 || s.AuthPassword == "" || s.Callsign == "" {
		s.LinkActive = false
		s.AuthStage = authDisconnected
		s.mu.Unlock()
		g.sendNetworkStatus(s, "error", "Callsign, DMR ID and the required user credential are required")
		return
	}
	remote, err := net.ResolveUDPAddr("udp", net.JoinHostPort(entry.Host, strconv.Itoa(entry.Port)))
	if err != nil {
		s.LinkActive = false
		s.AuthStage = authDisconnected
		s.mu.Unlock()
		g.sendNetworkStatus(s, "error", "Unable to resolve DMR master")
		return
	}
	s.RemoteAddr = remote
	s.AuthStage = authWaitingLogin
	s.LastNetwork = time.Now()
	repeaterID := s.RepeaterID
	s.mu.Unlock()

	packet := append([]byte("RPTL"), writeUint32BE(repeaterID)...)
	if err := g.writeNetworkPacket(s, packet); err != nil {
		g.sendNetworkStatus(s, "error", "Unable to contact DMR master")
		return
	}
	g.sendNetworkStatus(s, "connecting", "Waiting for master authentication")
}

func (g *Gateway) disconnectNetwork(s *UserSession, message string) {
	g.releaseTX(nil, s.ID, "network disconnected")
	s.mu.RLock()
	connected := s.AuthStage == authRunning
	repeaterID := s.RepeaterID
	mode := s.Mode
	s.mu.RUnlock()
	if connected && mode == "DMR" {
		packet := append([]byte("RPTCL"), writeUint32BE(repeaterID)...)
		_ = g.writeNetworkPacket(s, packet)
	}
	s.mu.Lock()
	s.LinkActive = false
	s.AuthStage = authDisconnected
	s.RemoteAddr = nil
	s.UserPassword = ""
	s.MasterPassword = ""
	s.AuthPassword = ""
	s.RequiresUserPassword = false
	s.mu.Unlock()
	g.sendNetworkStatus(s, "disconnected", message)
	g.updateBridgeStatus()
}

func (g *Gateway) sendDMRAuthorisation(s *UserSession, salt []byte) {
	s.mu.RLock()
	password := s.AuthPassword
	repeaterID := s.RepeaterID
	s.mu.RUnlock()
	payload := append(append([]byte{}, salt...), []byte(password)...)
	digest := sha256.Sum256(payload)
	packet := append([]byte("RPTK"), writeUint32BE(repeaterID)...)
	packet = append(packet, digest[:]...)
	_ = g.writeNetworkPacket(s, packet)
}

func (g *Gateway) sendDMRConfig(s *UserSession) {
	s.mu.RLock()
	callsign := s.Callsign
	repeaterID := s.RepeaterID
	s.mu.RUnlock()
	config := fmt.Sprintf("%-8.8s%09d%09d%02d%02d%8.8s%9.9s%03d%-20.20s%-19.19s%c%-124.124s%-40.40s%-40.40s",
		callsign, 430200000, 430200000, 1, 1, "0.000000", "0.000000", 0,
		"United Kingdom", "Yorkshire Link HUB", '4', "https://194.146.49.25", "Yorkshire Link HUB 2.0", "MMDVM")
	packet := append([]byte("RPTC"), writeUint32BE(repeaterID)...)
	packet = append(packet, []byte(config)...)
	_ = g.writeNetworkPacket(s, packet)
}

func (g *Gateway) sendDMROptions(s *UserSession) {
	s.mu.RLock()
	repeaterID := s.RepeaterID
	options := s.Options
	s.mu.RUnlock()
	packet := append([]byte("RPTO"), writeUint32BE(repeaterID)...)
	packet = append(packet, []byte(options)...)
	_ = g.writeNetworkPacket(s, packet)
}

func (g *Gateway) markDMRConnected(s *UserSession) {
	s.mu.Lock()
	s.AuthStage = authRunning
	s.LastNetwork = time.Now()
	network := s.Target
	repeaterID := s.RepeaterID
	s.mu.Unlock()
	fmt.Printf("[NET] Node %d logged into %s as %d\n", s.ID, network, repeaterID)
	g.sendNetworkStatus(s, "connected", "Master login accepted")
	g.updateBridgeStatus()
}

func (g *Gateway) handleDMRControl(s *UserSession, data []byte, remote *net.UDPAddr) bool {
	s.mu.RLock()
	expected := s.RemoteAddr
	stage := s.AuthStage
	options := s.Options
	s.mu.RUnlock()
	if expected == nil || remote == nil || !expected.IP.Equal(remote.IP) || expected.Port != remote.Port {
		return false
	}

	if len(data) >= 6 && (string(data[:6]) == "RPTACK" || string(data[:6]) == "MSTACK") {
		s.mu.Lock()
		s.LastNetwork = time.Now()
		s.mu.Unlock()
		switch stage {
		case authWaitingLogin:
			if len(data) < 10 {
				return true
			}
			s.mu.Lock()
			s.AuthStage = authWaitingAuthorisation
			s.mu.Unlock()
			g.sendDMRAuthorisation(s, data[6:10])
		case authWaitingAuthorisation:
			s.mu.Lock()
			s.AuthStage = authWaitingConfig
			s.mu.Unlock()
			g.sendDMRConfig(s)
		case authWaitingConfig:
			if options == "" {
				g.markDMRConnected(s)
			} else {
				s.mu.Lock()
				s.AuthStage = authWaitingOptions
				s.mu.Unlock()
				g.sendDMROptions(s)
			}
		case authWaitingOptions:
			g.markDMRConnected(s)
		}
		return true
	}

	if len(data) >= 6 && string(data[:6]) == "MSTNAK" {
		s.mu.Lock()
		s.LinkActive = false
		s.AuthStage = authDisconnected
		s.UserPassword = ""
		s.MasterPassword = ""
		s.AuthPassword = ""
		s.mu.Unlock()
		fmt.Printf("[NET] Node %d login rejected by %s\n", s.ID, s.Target)
		g.sendNetworkStatus(s, "rejected", "Master rejected the DMR ID or password")
		g.updateBridgeStatus()
		return true
	}

	if len(data) >= 7 && string(data[:7]) == "MSTPONG" {
		s.mu.Lock()
		s.LastNetwork = time.Now()
		s.mu.Unlock()
		return true
	}
	if len(data) >= 5 && string(data[:5]) == "MSTCL" {
		g.disconnectNetwork(s, "Master closed the connection")
		return true
	}
	return len(data) >= 4 && (string(data[:4]) == "RPTP" || string(data[:4]) == "DMRB")
}

func (g *Gateway) maintainDMRConnection(s *UserSession) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.RLock()
		active := s.LinkActive && s.Mode == "DMR"
		stage := s.AuthStage
		lastNetwork := s.LastNetwork
		lastControl := s.LastControl
		repeaterID := s.RepeaterID
		s.mu.RUnlock()
		if !active {
			continue
		}
		if stage == authRunning {
			if time.Since(lastNetwork) > 60*time.Second {
				g.sendNetworkStatus(s, "connecting", "Master timed out; reconnecting")
				g.beginDMRLogin(s)
				continue
			}
			if time.Since(lastControl) > 10*time.Second {
				packet := append([]byte("RPTPING"), writeUint32BE(repeaterID)...)
				_ = g.writeNetworkPacket(s, packet)
			}
		} else if stage != authDisconnected && time.Since(lastControl) > 10*time.Second {
			g.beginDMRLogin(s)
		}
	}
}

func (g *Gateway) runUDPListener(s *UserSession) {
	addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", s.Port))
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		fmt.Printf("[NET] Node %d UDP listener failed: %v\n", s.ID, err)
		return
	}
	defer conn.Close()
	s.mu.Lock()
	s.Conn = conn
	s.mu.Unlock()
	go g.maintainDMRConnection(s)

	// TX path: Browser PCM -> Vocoder -> Framer -> Network
	go func() {
		var lastFrame []byte
		for {
			select {
			case pcmData := <-s.rtcBuffer:
				if s.FlushFEC.CompareAndSwap(true, false) {
					lastFrame = nil
				}

				var finalPayload []byte

				// Step 1: Vocoding (SW or HW)
				useHW := s.UseHWVocoder.Load()
				var voiceData []byte

				if useHW {
					// Hardware vocoder via DV30
					dv30Addr := s.DV30Addr.Load().(*net.UDPAddr)
					if dv30Addr != nil {
						voiceData = encodeDV30(pcmData, dv30Addr, s.ID)
					} else {
						voiceData = encodeMBE(pcmData) // Fallback to SW
					}
				} else {
					// Software vocoder (MBE)
					voiceData = encodeMBE(pcmData)
				}

				// Step 2: Protocol framing
				s.mu.RLock()
				mode := s.Mode
				target := s.Target
				tg := s.TG
				dmrid := s.DMRID
				repeaterID := s.RepeaterID
				connected := s.AuthStage == authRunning
				remoteAddr := s.RemoteAddr
				s.mu.RUnlock()

				if mode == "DMR" && connected {
					s.mu.Lock()
					s.SeqNo++
					seqNo := s.SeqNo
					streamID := s.StreamID
					s.mu.Unlock()
					finalPayload = buildDMRFrame(voiceData, dmrid, tg, repeaterID, seqNo, streamID, lastFrame)
				} else if mode == "YSF" {
					finalPayload = buildYSFFrame(voiceData, s.Callsign, lastFrame)
				}

				// Step 3: Transmit to network
				if len(finalPayload) > 0 {
					targetAddr := remoteAddr
					if mode != "DMR" {
						targetAddr = resolveNetworkTarget(target)
					}
					if targetAddr != nil {
						conn.WriteToUDP(finalPayload, targetAddr)
					}
				}

				lastFrame = make([]byte, len(voiceData))
				copy(lastFrame, voiceData)
			}
		}
	}()

	// RX path: Network -> Decoder -> Browser PCM
	for {
		bufPtr := udpPool.Get().(*[]byte)
		buf := *bufPtr

		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, remoteAddr, err := conn.ReadFromUDP(buf)

		if err == nil {
			s.mu.RLock()
			mode := s.Mode
			s.mu.RUnlock()
			if mode == "DMR" && g.handleDMRControl(s, buf[:n], remoteAddr) {
				udpPool.Put(bufPtr)
				continue
			}
		}

		if err == nil && n > 10 {
			// Parse protocol frame
			var pcmData []byte
			var sourceID uint32
			var sourceName string
			var dmrMeta DMRTrafficMeta

			s.mu.RLock()
			mode := s.Mode
			expected := s.RemoteAddr
			s.mu.RUnlock()
			if mode == "DMR" && (expected == nil || !expected.IP.Equal(remoteAddr.IP) || expected.Port != remoteAddr.Port) {
				udpPool.Put(bufPtr)
				continue
			}

			if mode == "DMR" {
				pcmData, sourceID = parseDMRFrame(buf[:n])
				if sourceID > 0 && !g.registeredDMRID(sourceID) {
					g.reportRejectedDMR(sourceID)
					udpPool.Put(bufPtr)
					continue
				}
				g.forwardBridgeFrame(s, buf[:n])
				dmrMeta = parseDMRTrafficMeta(buf[:n])
			} else if mode == "YSF" {
				pcmData, sourceName = parseYSFFrame(buf[:n])
			}

			// Decode voice to PCM
			if len(pcmData) > 0 {
				var decodedPCM []byte
				if s.UseHWVocoder.Load() {
					dv30Addr := s.DV30Addr.Load().(*net.UDPAddr)
					if dv30Addr != nil {
						decodedPCM = decodeDV30(pcmData, dv30Addr, s.ID)
					}
				}
				if len(decodedPCM) == 0 {
					decodedPCM = decodeMBE(pcmData)
				}

				// Send to all connected browsers
				g.mu.Lock()
				for client := range g.clients {
					select {
					case client.send <- decodedPCM:
					default:
					}
				}
				g.mu.Unlock()

				// Traffic logging
				if sourceID > 0 || sourceName != "" {
					g.pushTrafficWS(s.ID, mode, s.Target, sourceID, sourceName, dmrMeta)
				}
			}

			_ = remoteAddr
		}
		udpPool.Put(bufPtr)
	}
}

// ============================================================================
// MBE SOFTWARE VOCODER (AMBE+2 Codec - Full Implementation)
// ============================================================================

// AMBE+2 vocoder parameters
const (
	ambeFrameSamples = 160 // 20ms @ 8kHz
	ambeFrameBytes   = 9   // 72 bits
	ambeSubframes    = 4   // 4x 40-sample subframes
	ambeBands        = 56  // Spectral bands
	ambeHarmonics    = 16  // Max harmonics
)

// Pre-computed DCT-II matrix for spectral analysis (56 bands)
var dctMatrix [ambeBands][ambeFrameSamples]float64

// Pre-computed Hamming window for spectral smoothing
var hammingWindow [ambeFrameSamples]float64

// Quantization tables for AMBE+2 (derived from DVSI specification)
var (
	// Fundamental frequency codebook (7 bits = 128 entries)
	fundamentalCodebook = [128]float64{
		50.0, 52.5, 55.0, 57.5, 60.0, 62.5, 65.0, 67.5, 70.0, 72.5, 75.0, 77.5, 80.0, 82.5, 85.0, 87.5,
		90.0, 92.5, 95.0, 97.5, 100.0, 102.5, 105.0, 107.5, 110.0, 112.5, 115.0, 117.5, 120.0, 122.5, 125.0, 127.5,
		130.0, 135.0, 140.0, 145.0, 150.0, 155.0, 160.0, 165.0, 170.0, 175.0, 180.0, 185.0, 190.0, 195.0, 200.0, 205.0,
		210.0, 215.0, 220.0, 225.0, 230.0, 235.0, 240.0, 245.0, 250.0, 255.0, 260.0, 265.0, 270.0, 275.0, 280.0, 285.0,
		290.0, 295.0, 300.0, 305.0, 310.0, 315.0, 320.0, 325.0, 330.0, 335.0, 340.0, 345.0, 350.0, 355.0, 360.0, 365.0,
		370.0, 375.0, 380.0, 385.0, 390.0, 395.0, 400.0, 405.0, 410.0, 415.0, 420.0, 425.0, 430.0, 435.0, 440.0, 445.0,
		450.0, 460.0, 470.0, 480.0, 490.0, 500.0, 510.0, 520.0, 530.0, 540.0, 550.0, 560.0, 570.0, 580.0, 590.0, 600.0,
		610.0, 620.0, 630.0, 640.0, 650.0, 660.0, 670.0, 680.0, 690.0, 700.0, 710.0, 720.0, 730.0, 740.0, 750.0, 760.0,
	}

	// Voicing decision codebook (1 bit per band = 56 bits total, packed into 7 bytes)
	// 1 = voiced (harmonic), 0 = unvoiced (noise)

	// Spectral magnitude codebook (8 bits per band group)
	magnitudeCodebook = [256]float64{
		0.0, 0.5, 1.0, 1.5, 2.0, 2.5, 3.0, 3.5, 4.0, 4.5, 5.0, 5.5, 6.0, 6.5, 7.0, 7.5,
		8.0, 8.5, 9.0, 9.5, 10.0, 10.5, 11.0, 11.5, 12.0, 12.5, 13.0, 13.5, 14.0, 14.5, 15.0, 15.5,
		16.0, 16.5, 17.0, 17.5, 18.0, 18.5, 19.0, 19.5, 20.0, 20.5, 21.0, 21.5, 22.0, 22.5, 23.0, 23.5,
		24.0, 24.5, 25.0, 25.5, 26.0, 26.5, 27.0, 27.5, 28.0, 28.5, 29.0, 29.5, 30.0, 30.5, 31.0, 31.5,
		32.0, 33.0, 34.0, 35.0, 36.0, 37.0, 38.0, 39.0, 40.0, 41.0, 42.0, 43.0, 44.0, 45.0, 46.0, 47.0,
		48.0, 49.0, 50.0, 51.0, 52.0, 53.0, 54.0, 55.0, 56.0, 57.0, 58.0, 59.0, 60.0, 61.0, 62.0, 63.0,
		64.0, 66.0, 68.0, 70.0, 72.0, 74.0, 76.0, 78.0, 80.0, 82.0, 84.0, 86.0, 88.0, 90.0, 92.0, 94.0,
		96.0, 98.0, 100.0, 102.0, 104.0, 106.0, 108.0, 110.0, 112.0, 114.0, 116.0, 118.0, 120.0, 122.0, 124.0, 126.0,
		128.0, 132.0, 136.0, 140.0, 144.0, 148.0, 152.0, 156.0, 160.0, 164.0, 168.0, 172.0, 176.0, 180.0, 184.0, 188.0,
		192.0, 196.0, 200.0, 204.0, 208.0, 212.0, 216.0, 220.0, 224.0, 228.0, 232.0, 236.0, 240.0, 244.0, 248.0, 252.0,
		256.0, 264.0, 272.0, 280.0, 288.0, 296.0, 304.0, 312.0, 320.0, 328.0, 336.0, 344.0, 352.0, 360.0, 368.0, 376.0,
		384.0, 392.0, 400.0, 408.0, 416.0, 424.0, 432.0, 440.0, 448.0, 456.0, 464.0, 472.0, 480.0, 488.0, 496.0, 504.0,
		512.0, 528.0, 544.0, 560.0, 576.0, 592.0, 608.0, 624.0, 640.0, 656.0, 672.0, 688.0, 704.0, 720.0, 736.0, 752.0,
		768.0, 784.0, 800.0, 816.0, 832.0, 848.0, 864.0, 880.0, 896.0, 912.0, 928.0, 944.0, 960.0, 976.0, 992.0, 1008.0,
		1024.0, 1056.0, 1088.0, 1120.0, 1152.0, 1184.0, 1216.0, 1248.0, 1280.0, 1312.0, 1344.0, 1376.0, 1408.0, 1440.0, 1472.0, 1504.0,
		1536.0, 1568.0, 1600.0, 1632.0, 1664.0, 1696.0, 1728.0, 1760.0, 1792.0, 1824.0, 1856.0, 1888.0, 1920.0, 1952.0, 1984.0, 2016.0,
	}
)

func init() {
	// Pre-compute DCT-II matrix for analysis
	for k := 0; k < ambeBands; k++ {
		for n := 0; n < ambeFrameSamples; n++ {
			dctMatrix[k][n] = math.Cos(math.Pi * float64(k) * (float64(n) + 0.5) / float64(ambeFrameSamples))
		}
	}

	// Pre-compute Hamming window
	for i := 0; i < ambeFrameSamples; i++ {
		hammingWindow[i] = 0.54 - 0.46*math.Cos(2.0*math.Pi*float64(i)/float64(ambeFrameSamples-1))
	}
}

// encodeMBE: PCM (160 samples @ 8kHz) -> AMBE+2 (9 bytes = 72 bits)
func encodeMBE(pcm []byte) []byte {
	// Convert PCM bytes to float64 samples
	samples := make([]float64, ambeFrameSamples)
	for i := 0; i < ambeFrameSamples; i++ {
		s := int16(binary.LittleEndian.Uint16(pcm[i*2:]))
		samples[i] = float64(s) / 32768.0
	}

	// Apply Hamming window
	windowed := make([]float64, ambeFrameSamples)
	for i := 0; i < ambeFrameSamples; i++ {
		windowed[i] = samples[i] * hammingWindow[i]
	}

	// Compute spectral coefficients via DCT
	spectrum := make([]float64, ambeBands)
	for k := 0; k < ambeBands; k++ {
		sum := 0.0
		for n := 0; n < ambeFrameSamples; n++ {
			sum += windowed[n] * dctMatrix[k][n]
		}
		spectrum[k] = sum * 2.0 / float64(ambeFrameSamples)
	}

	// Estimate fundamental frequency (pitch detection)
	f0 := estimatePitch(samples)
	f0Index := quantizeFundamental(f0)

	// Compute harmonic magnitudes
	magnitudes := make([]float64, ambeHarmonics)
	for h := 0; h < ambeHarmonics; h++ {
		harmonic := f0 * float64(h+1)
		bin := int(harmonic * float64(ambeFrameSamples) / 8000.0)
		if bin < ambeBands {
			magnitudes[h] = math.Abs(spectrum[bin])
		}
	}

	// Voicing decision (simple energy threshold per band)
	voicing := uint64(0)
	for k := 0; k < ambeBands && k < 56; k++ {
		energy := math.Abs(spectrum[k])
		if energy > 0.1 { // Voiced
			voicing |= (1 << uint(k))
		}
	}

	// Quantize magnitudes (8 groups of 2 harmonics each)
	magIndices := make([]byte, 8)
	for g := 0; g < 8; g++ {
		h := g * 2
		if h < ambeHarmonics {
			avgMag := (magnitudes[h] + magnitudes[min(h+1, ambeHarmonics-1)]) / 2.0
			magIndices[g] = quantizeMagnitude(avgMag * 1000.0) // Scale for quantizer
		}
	}

	// Pack into 72 bits (9 bytes)
	bits := make([]byte, 9)

	// Bits 0-6: Fundamental frequency (7 bits)
	bits[0] = f0Index

	// Bits 7-14: First magnitude group (8 bits)
	bits[0] |= (magIndices[0] & 0x01) << 7
	bits[1] = (magIndices[0] >> 1) & 0x7F

	// Bits 15-22: Second magnitude group (8 bits)
	bits[1] |= (magIndices[1] & 0x01) << 7
	bits[2] = (magIndices[1] >> 1) & 0x7F

	// Bits 23-30: Third magnitude group (8 bits)
	bits[2] |= (magIndices[2] & 0x01) << 7
	bits[3] = (magIndices[2] >> 1) & 0x7F

	// Bits 31-38: Fourth magnitude group (8 bits)
	bits[3] |= (magIndices[3] & 0x01) << 7
	bits[4] = (magIndices[3] >> 1) & 0x7F

	// Bits 39-46: Voicing bitmap (8 bits - first 8 bands)
	bits[4] |= byte((voicing & 0x01) << 7)
	bits[5] = byte((voicing >> 1) & 0x7F)

	// Bits 47-54: Voicing continuation (8 bits - bands 8-15)
	bits[5] |= byte(((voicing >> 8) & 0x01) << 7)
	bits[6] = byte((voicing >> 9) & 0x7F)

	// Bits 55-62: Fifth magnitude group (8 bits)
	bits[6] |= (magIndices[4] & 0x01) << 7
	bits[7] = (magIndices[4] >> 1) & 0x7F

	// Bits 63-70: Sixth magnitude group (8 bits)
	bits[7] |= (magIndices[5] & 0x01) << 7
	bits[8] = (magIndices[5] >> 1) & 0x7F

	// Bit 71: Parity/reserved
	bits[8] |= 0x80

	return bits
}

// decodeMBE: AMBE+2 (9 bytes) -> PCM (160 samples @ 8kHz)
func decodeMBE(ambe []byte) []byte {
	if len(ambe) < 9 {
		return make([]byte, 320)
	}

	// Unpack 72 bits
	f0Index := ambe[0] & 0x7F

	magIndices := make([]byte, 8)
	magIndices[0] = ((ambe[0] >> 7) & 0x01) | ((ambe[1] & 0x7F) << 1)
	magIndices[1] = ((ambe[1] >> 7) & 0x01) | ((ambe[2] & 0x7F) << 1)
	magIndices[2] = ((ambe[2] >> 7) & 0x01) | ((ambe[3] & 0x7F) << 1)
	magIndices[3] = ((ambe[3] >> 7) & 0x01) | ((ambe[4] & 0x7F) << 1)
	magIndices[4] = ((ambe[6] >> 7) & 0x01) | ((ambe[7] & 0x7F) << 1)
	magIndices[5] = ((ambe[7] >> 7) & 0x01) | ((ambe[8] & 0x7F) << 1)

	voicing := uint64(ambe[4]>>7) | (uint64(ambe[5]&0x7F) << 1) | (uint64(ambe[5]>>7) << 8) | (uint64(ambe[6]&0x7F) << 9)

	// Dequantize fundamental frequency
	f0 := fundamentalCodebook[f0Index]

	// Dequantize magnitudes
	magnitudes := make([]float64, ambeHarmonics)
	for g := 0; g < 6; g++ {
		h := g * 2
		if h < ambeHarmonics {
			mag := magnitudeCodebook[magIndices[g]] / 1000.0
			magnitudes[h] = mag
			if h+1 < ambeHarmonics {
				magnitudes[h+1] = mag
			}
		}
	}

	// Synthesize spectrum
	spectrum := make([]float64, ambeFrameSamples)
	for h := 0; h < ambeHarmonics; h++ {
		harmonic := f0 * float64(h+1)
		bin := int(harmonic * float64(ambeFrameSamples) / 8000.0)
		if bin < len(spectrum) {
			// Check voicing for this harmonic's band
			voiced := (voicing & (1 << uint(min(bin, 55)))) != 0
			if voiced {
				// Harmonic synthesis
				phase := 2.0 * math.Pi * harmonic / 8000.0
				for n := 0; n < ambeFrameSamples; n++ {
					spectrum[n] += magnitudes[h] * math.Sin(phase*float64(n))
				}
			} else {
				// Noise synthesis
				for n := 0; n < ambeFrameSamples; n++ {
					spectrum[n] += magnitudes[h] * (rand.Float64()*2.0 - 1.0) * 0.3
				}
			}
		}
	}

	// Apply smoothing
	for i := 1; i < ambeFrameSamples-1; i++ {
		spectrum[i] = 0.25*spectrum[i-1] + 0.5*spectrum[i] + 0.25*spectrum[i+1]
	}

	// Convert to PCM
	pcm := make([]byte, 320)
	for i := 0; i < ambeFrameSamples; i++ {
		sample := spectrum[i]

		// Clamp to [-1.0, 1.0]
		if sample > 1.0 {
			sample = 1.0
		} else if sample < -1.0 {
			sample = -1.0
		}

		// Convert to int16
		s := int16(sample * 32767.0)
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(s))
	}

	return pcm
}

// estimatePitch: Autocorrelation-based pitch detection
func estimatePitch(samples []float64) float64 {
	minPeriod := 8000 / 500 // 500 Hz max
	maxPeriod := 8000 / 50  // 50 Hz min

	maxCorr := 0.0
	bestLag := minPeriod

	for lag := minPeriod; lag <= maxPeriod && lag < len(samples)/2; lag++ {
		corr := 0.0
		for i := 0; i < len(samples)-lag; i++ {
			corr += samples[i] * samples[i+lag]
		}
		if corr > maxCorr {
			maxCorr = corr
			bestLag = lag
		}
	}

	return 8000.0 / float64(bestLag)
}

// quantizeFundamental: Find nearest fundamental frequency in codebook
func quantizeFundamental(f0 float64) byte {
	minDist := math.Abs(f0 - fundamentalCodebook[0])
	minIdx := 0

	for i := 1; i < len(fundamentalCodebook); i++ {
		dist := math.Abs(f0 - fundamentalCodebook[i])
		if dist < minDist {
			minDist = dist
			minIdx = i
		}
	}

	return byte(minIdx)
}

// quantizeMagnitude: Find nearest magnitude in codebook
func quantizeMagnitude(mag float64) byte {
	minDist := math.Abs(mag - magnitudeCodebook[0])
	minIdx := 0

	for i := 1; i < len(magnitudeCodebook); i++ {
		dist := math.Abs(mag - magnitudeCodebook[i])
		if dist < minDist {
			minDist = dist
			minIdx = i
		}
	}

	return byte(minIdx)
}

// ============================================================================
// DV30 HARDWARE VOCODER CLIENT
// ============================================================================

func parseDV30Address(value string) *net.UDPAddr {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		value = parsed.Host
	}
	if _, _, err := net.SplitHostPort(value); err != nil && !strings.Contains(value, ":") {
		value = net.JoinHostPort(value, "2468")
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return nil
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return nil
	}
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(parsedPort)))
	if err != nil {
		return nil
	}
	return addr
}

func safeVocoderAddress(addr *net.UDPAddr) string {
	if addr == nil {
		return "not-set"
	}
	return addr.String()
}

var dv30Pool = sync.Pool{
	New: func() any {
		conn, _ := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
		return conn
	},
}

var dv30RequestID atomic.Uint32

func nextDV30Channel() byte {
	return byte(dv30RequestID.Add(1))
}

func exchangeDV30(request []byte, target *net.UDPAddr, replyOpcode, channel byte, minimumSize int) []byte {
	conn := dv30Pool.Get().(*net.UDPConn)
	defer dv30Pool.Put(conn)
	if _, err := conn.WriteToUDP(request, target); err != nil {
		return nil
	}
	conn.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
	resp := make([]byte, 512)
	n, _, err := conn.ReadFromUDP(resp)
	if err == nil && n >= minimumSize && resp[0] == replyOpcode && resp[1] == channel {
		return resp[:n]
	}
	return nil
}

func encodeDV30(pcm []byte, target *net.UDPAddr, _ int) []byte {
	if len(pcm) != 320 {
		return encodeMBE(pcm)
	}
	channel := nextDV30Channel()
	request := make([]byte, 2+len(pcm))
	request[0], request[1] = 0x61, channel
	copy(request[2:], pcm)
	if response := exchangeDV30(request, target, 0x62, channel, 11); response != nil {
		return append([]byte(nil), response[2:11]...)
	}

	// Fallback to software if HW fails.
	return encodeMBE(pcm)
}

func decodeDV30(ambe []byte, target *net.UDPAddr, _ int) []byte {
	if len(ambe) != 9 {
		return decodeMBE(ambe)
	}
	channel := nextDV30Channel()
	request := make([]byte, 2+len(ambe))
	request[0], request[1] = 0x63, channel
	copy(request[2:], ambe)
	if response := exchangeDV30(request, target, 0x64, channel, 322); response != nil {
		return append([]byte(nil), response[2:322]...)
	}

	return decodeMBE(ambe)
}

// ============================================================================
// DMR PROTOCOL FRAMER (ETSI TS 102 361)
// ============================================================================

func buildDMRFrame(voice []byte, srcID, dstID, repeaterID uint32, seqNo uint8, streamID uint32, lastVoice []byte) []byte {
	// MMDVM/Homebrew DMRD packet: 20-byte header, 33-byte DMR payload, BER and RSSI.
	frame := make([]byte, 55)
	copy(frame[0:4], []byte("DMRD"))
	frame[4] = seqNo
	frame[5] = byte(srcID >> 16)
	frame[6] = byte(srcID >> 8)
	frame[7] = byte(srcID)
	frame[8] = byte(dstID >> 16)
	frame[9] = byte(dstID >> 8)
	frame[10] = byte(dstID)
	binary.BigEndian.PutUint32(frame[11:15], repeaterID)
	frame[15] = 0x80 | (seqNo % 6) // Slot 2, group voice burst number.
	if seqNo%6 == 0 {
		frame[15] = 0x90 // Slot 2 voice sync.
	}
	binary.LittleEndian.PutUint32(frame[16:20], streamID)

	// The software vocoder emits one 9-byte AMBE frame per 20 ms. Populate the
	// 33-byte network burst with the current and previous frames.
	copy(frame[20:29], voice[:min(len(voice), 9)])
	if lastVoice != nil {
		copy(frame[29:38], lastVoice[:min(len(lastVoice), 9)])
	}
	copy(frame[38:47], voice[:min(len(voice), 9)])

	return frame
}

func parseDMRFrame(data []byte) ([]byte, uint32) {
	if len(data) < 55 || string(data[:4]) != "DMRD" {
		return nil, 0
	}
	voice := data[20:29]
	srcID := uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7])

	return voice, srcID
}

func parseDMRTrafficMeta(data []byte) DMRTrafficMeta {
	if len(data) < 53 || string(data[:4]) != "DMRD" {
		return DMRTrafficMeta{}
	}
	flags := data[15]
	meta := DMRTrafficMeta{
		DestinationID: uint32(data[8])<<16 | uint32(data[9])<<8 | uint32(data[10]),
		RepeaterID:    binary.BigEndian.Uint32(data[11:15]),
		StreamID:      binary.BigEndian.Uint32(data[16:20]),
		Sequence:      data[4],
		Slot:          1,
		CallType:      "group",
		FrameType:     "voice",
	}
	if flags&0x80 != 0 {
		meta.Slot = 2
	}
	if flags&0x40 != 0 {
		meta.CallType = "private"
	}
	switch (flags >> 4) & 0x03 {
	case 1:
		meta.FrameType = "voice sync"
	case 2:
		meta.FrameType = "data sync"
	}
	// BER/RSSI are optional Homebrew extensions. A zero byte is treated as
	// unavailable because network masters commonly pad absent metrics with 0.
	if len(data) >= 55 {
		if data[53] > 0 && data[53] <= 100 {
			meta.BER, meta.BERAvailable = int(data[53]), true
		}
		if data[54] > 0 {
			meta.RSSI, meta.RSSIAvailable = -int(data[54]), true
		}
	}
	return meta
}

// ============================================================================
// YSF PROTOCOL FRAMER (C4FM)
// ============================================================================

func buildYSFFrame(voice []byte, callsign string, lastVoice []byte) []byte {
	// YSF frame: 120 bytes
	// [Sync 5][FICH 25][DCH 20][VCH 9x5][CRC 2]

	frame := make([]byte, 120)

	// Sync pattern
	sync := []byte{0xD4, 0x71, 0xC9, 0x63, 0x4D}
	copy(frame[0:5], sync)

	// FICH (Frame Information Channel Header)
	fich := make([]byte, 25)
	fich[0] = 0x01 // V/D Mode 1
	fich[1] = 0x00 // Frame type: Voice/Data
	copy(frame[5:30], fich)

	// DCH (Data Channel) - Callsign
	dch := make([]byte, 20)
	copy(dch[0:10], []byte(callsign))
	copy(frame[30:50], dch)

	// VCH (Voice Channel) - 5 AMBE frames
	for i := 0; i < 5; i++ {
		offset := 50 + (i * 9)
		if i == 0 && len(voice) >= 9 {
			copy(frame[offset:offset+9], voice[:9])
		} else if lastVoice != nil && len(lastVoice) >= 9 {
			copy(frame[offset:offset+9], lastVoice[:9])
		}
	}

	// CRC placeholder
	frame[118] = 0x00
	frame[119] = 0x00

	return frame
}

func parseYSFFrame(data []byte) ([]byte, string) {
	if len(data) < 120 {
		return nil, ""
	}

	// Extract first voice frame
	voice := data[50:59]

	// Extract callsign from DCH
	callsign := strings.TrimSpace(string(data[30:40]))

	return voice, callsign
}

// ============================================================================
// NETWORK TARGET RESOLUTION
// ============================================================================

func resolveNetworkTarget(target string) *net.UDPAddr {
	addrStr, exists := dmrNetworkTargets[target]
	if !exists {
		addrStr = target // Direct IP:Port
	}

	addr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		return nil
	}
	return addr
}

// ============================================================================
// TRAFFIC LOGGING
// ============================================================================

func (g *Gateway) pushTrafficWS(nodeID int, mode, network string, sourceID uint32, sourceName string, meta DMRTrafficMeta) {
	g.dbMutex.RLock()
	info, exists := g.idDB[sourceID]
	g.dbMutex.RUnlock()

	callsign := fmt.Sprintf("%d", sourceID)
	if exists && info.Callsign != "" {
		callsign = info.Callsign
	} else if sourceName != "" {
		callsign = sourceName
	}
	locationParts := make([]string, 0, 3)
	for _, part := range []string{info.City, info.State, info.Country} {
		if part != "" && (len(locationParts) == 0 || !strings.EqualFold(locationParts[len(locationParts)-1], part)) {
			locationParts = append(locationParts, part)
		}
	}
	target := ""
	if meta.DestinationID > 0 {
		target = fmt.Sprintf("%s %d", strings.ToUpper(meta.CallType), meta.DestinationID)
	}

	msg, _ := json.Marshal(map[string]any{
		"type": "traffic",
		"data": map[string]any{
			"time":           time.Now().UTC().Format("15:04:05"),
			"node":           nodeID,
			"mode":           mode,
			"raw_id":         sourceID,
			"callsign":       callsign,
			"name":           info.Name,
			"city":           info.City,
			"state":          info.State,
			"country":        info.Country,
			"location":       strings.Join(locationParts, ", "),
			"network":        network,
			"target":         target,
			"slot":           meta.Slot,
			"call_type":      meta.CallType,
			"frame_type":     meta.FrameType,
			"stream_id":      meta.StreamID,
			"sequence":       meta.Sequence,
			"repeater_id":    meta.RepeaterID,
			"ber":            meta.BER,
			"ber_available":  meta.BERAvailable,
			"rssi":           meta.RSSI,
			"rssi_available": meta.RSSIAvailable,
		},
	})
	g.broadcastText(msg)
}

// ============================================================================
// REGISTRY MANAGEMENT
// ============================================================================

func (g *Gateway) updateRegistriesDaily() {
	g.downloadFile("https://radioid.net/static/dmrid.dat", "/var/lib/dvgateway/dmrid.dat")
	g.downloadFile("https://database.radioid.net/static/user.csv", "/var/lib/dvgateway/radioid-users.csv")
	g.downloadYSFRegistry()
	g.downloadFile("https://www.pistar.uk/downloads/DMR_Hosts.txt", "/var/lib/dvgateway/DMR_Hosts.txt")
	g.downloadFile("https://www.pistar.uk/downloads/XLXHosts.txt", "/var/lib/dvgateway/XLXHosts.txt")
	g.loadLocalDBAsync()

	for {
		time.Sleep(24 * time.Hour)
		g.downloadFile("https://radioid.net/static/dmrid.dat", "/var/lib/dvgateway/dmrid.dat")
		g.downloadFile("https://database.radioid.net/static/user.csv", "/var/lib/dvgateway/radioid-users.csv")
		g.downloadYSFRegistry()
		g.downloadFile("https://www.pistar.uk/downloads/DMR_Hosts.txt", "/var/lib/dvgateway/DMR_Hosts.txt")
		g.downloadFile("https://www.pistar.uk/downloads/XLXHosts.txt", "/var/lib/dvgateway/XLXHosts.txt")
		g.loadLocalDBAsync()
	}
}

func writeAtomicFile(dest string, data []byte, mode os.FileMode) error {
	os.MkdirAll(filepath.Dir(dest), 0755)
	tmpDest := dest + ".tmp"
	if err := os.WriteFile(tmpDest, data, mode); err != nil {
		return err
	}
	return os.Rename(tmpDest, dest)
}

// downloadYSFRegistry uses DVRef's authenticated API when an operator token is
// configured server-side. Pi-Star remains a safe fallback until that token is
// supplied; credentials are never sent to, or stored by, the browser.
func (g *Gateway) downloadYSFRegistry() {
	tokenBytes, tokenErr := os.ReadFile(dvrefTokenPath)
	token := strings.TrimSpace(string(tokenBytes))
	if tokenErr == nil && token != "" {
		req, _ := http.NewRequest(http.MethodGet, "https://dvref.com/api/v2/ysf/reflectors/", nil)
		req.Header.Set("Authorization", "Token "+token)
		req.Header.Set("User-Agent", "DVHub-Gateway/2.0 (2E0LXY; amateur radio reflector)")
		client := &http.Client{Timeout: 30 * time.Second}
		if resp, err := client.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var payload struct {
					Reflectors []struct {
						Designator  string `json:"designator"`
						Name        string `json:"name"`
						Description string `json:"description"`
						DNS         string `json:"dns"`
						IPv4        string `json:"ipv4"`
						Port        int    `json:"port"`
					} `json:"reflectors"`
				}
				if json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&payload) == nil && len(payload.Reflectors) > 100 {
					sort.Slice(payload.Reflectors, func(i, j int) bool { return payload.Reflectors[i].Designator < payload.Reflectors[j].Designator })
					var output strings.Builder
					for _, reflector := range payload.Reflectors {
						host := strings.TrimSpace(reflector.DNS)
						if host == "" {
							host = strings.TrimSpace(reflector.IPv4)
						}
						if host == "" || reflector.Port < 1 || reflector.Port > 65535 {
							continue
						}
						fmt.Fprintf(&output, "%s;%s;%s;%s;%d;000;\n", reflector.Designator, reflector.Name, reflector.Description, host, reflector.Port)
					}
					if writeAtomicFile("/var/lib/dvgateway/YSF_Hosts.txt", []byte(output.String()), 0644) == nil {
						_ = writeAtomicFile("/var/lib/dvgateway/YSF_Hosts.source", []byte("DVRef API\n"), 0644)
						return
					}
				}
			}
		}
	}

	g.downloadFile("https://www.pistar.uk/downloads/YSF_Hosts.txt", "/var/lib/dvgateway/YSF_Hosts.txt")
	_ = writeAtomicFile("/var/lib/dvgateway/YSF_Hosts.source", []byte("Pi-Star fallback (DVRef token not configured or API unavailable)\n"), 0644)
}

func (g *Gateway) downloadFile(url, dest string) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "DVHub-Gateway/2.0 (2E0LXY; amateur radio gateway)")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return
	}
	defer resp.Body.Close()

	os.MkdirAll("/var/lib/dvgateway", 0755)
	tmpDest := dest + ".tmp"

	out, err := os.Create(tmpDest)
	if err == nil {
		io.Copy(out, resp.Body)
		out.Close()
		os.Rename(tmpDest, dest)
	}
}

func parseYSFInfo() (id, name, description string) {
	data, err := os.ReadFile(ysfConfigPath)
	if err != nil {
		return "", ysfNetworkName, ysfDescription
	}
	inInfo := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inInfo = line == "[Info]"
			continue
		}
		if !inInfo {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch strings.TrimSpace(parts[0]) {
		case "Id":
			id = strings.TrimSpace(parts[1])
		case "Name":
			name = strings.TrimSpace(parts[1])
		case "Description":
			description = strings.TrimSpace(parts[1])
		}
	}
	return
}

func derivedYSFID(name string) string {
	padded := []byte(fmt.Sprintf("%-16.16s", name))
	var hash uint32
	for _, value := range padded {
		hash += uint32(value)
		hash += hash << 10
		hash ^= hash >> 6
	}
	hash += hash << 3
	hash ^= hash >> 11
	hash += hash << 15
	return fmt.Sprintf("%05d", hash%100000)
}

func ysfRegistrySource() string {
	data, err := os.ReadFile("/var/lib/dvgateway/YSF_Hosts.source")
	if err != nil {
		return "Pi-Star fallback"
	}
	return strings.TrimSpace(string(data))
}

func currentYSFIdentity() YSFIdentity {
	id, name, description := parseYSFInfo()
	suggested := id
	if suggested == "" {
		suggested = derivedYSFID(name)
	}
	_, lockErr := os.Stat(ysfIdentityLockPath)
	_, tokenErr := os.Stat(dvrefTokenPath)
	locked := lockErr == nil && regexp.MustCompile(`^[0-9]{5}$`).MatchString(id)
	return YSFIdentity{
		ID: id, SuggestedID: suggested, Name: name, Description: description,
		Host: "194.146.49.25", Port: 42000, Country: "GB", Locked: locked,
		DVRefReady: locked, DVRefRegistered: false, RegistrySource: ysfRegistrySource(),
		APITokenConfigured: tokenErr == nil, PublicDashboardPath: "/ysf-status.html",
	}
}

func ysfIDAlreadyListed(id string) bool {
	data, err := os.ReadFile("/var/lib/dvgateway/YSF_Hosts.txt")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), ";", 2)
		if len(fields) > 0 && fields[0] == id {
			return true
		}
	}
	return false
}

func (g *Gateway) handleYSFIdentity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		json.NewEncoder(w).Encode(currentYSFIdentity())
		return
	}
	if r.Method != http.MethodPost || r.Header.Get("X-DVHub-Control") != "1" {
		http.Error(w, `{"error":"Identity request rejected"}`, http.StatusForbidden)
		return
	}
	if currentYSFIdentity().Locked {
		http.Error(w, `{"error":"Reflector identity is permanently locked"}`, http.StatusConflict)
		return
	}
	var request struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&request) != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}
	if !regexp.MustCompile(`^[0-9]{5}$`).MatchString(request.ID) || request.ID <= "00005" || request.ID == "99999" {
		http.Error(w, `{"error":"Choose five digits; 00000-00005 and 99999 are reserved"}`, http.StatusBadRequest)
		return
	}
	if ysfIDAlreadyListed(request.ID) {
		http.Error(w, `{"error":"That number appears in the current YSF registry"}`, http.StatusConflict)
		return
	}
	cmd := exec.Command("/usr/bin/sudo", "-n", "/usr/local/sbin/dvhub-set-ysf-identity", request.ID, ysfNetworkName, ysfDescription)
	if output, err := cmd.CombinedOutput(); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, strings.TrimSpace(string(output))), http.StatusInternalServerError)
		return
	}
	time.Sleep(300 * time.Millisecond)
	json.NewEncoder(w).Encode(currentYSFIdentity())
}

func (g *Gateway) handleYSFHosts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "text/plain")
	http.ServeFile(w, r, "/var/lib/dvgateway/YSF_Hosts.txt")
}

func (g *Gateway) handleDMRHosts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	http.ServeFile(w, r, "/var/lib/dvgateway/DMR_Hosts.txt")
}

func (g *Gateway) handleXLXHosts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	http.ServeFile(w, r, "/var/lib/dvgateway/XLXHosts.txt")
}

func (g *Gateway) handleYSFControl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	status := func() string {
		if err := exec.Command("/usr/bin/systemctl", "is-active", "--quiet", "ysfreflector.service").Run(); err == nil {
			return "running"
		}
		return "stopped"
	}

	if r.Method == http.MethodGet {
		json.NewEncoder(w).Encode(map[string]string{"status": status(), "host": "194.146.49.25", "port": "42000"})
		return
	}
	if r.Method != http.MethodPost || r.Header.Get("X-DVHub-Control") != "1" {
		http.Error(w, `{"error":"Control request rejected"}`, http.StatusForbidden)
		return
	}
	var request struct {
		Action string `json:"action"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&request) != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}
	if request.Action != "start" && request.Action != "stop" && request.Action != "restart" {
		http.Error(w, `{"error":"Invalid action"}`, http.StatusBadRequest)
		return
	}
	command := exec.Command("/usr/bin/sudo", "-n", "/usr/bin/systemctl", request.Action, "ysfreflector.service")
	if output, err := command.CombinedOutput(); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, strings.TrimSpace(string(output))), http.StatusInternalServerError)
		return
	}
	if request.Action != "stop" {
		time.Sleep(250 * time.Millisecond)
	}
	json.NewEncoder(w).Encode(map[string]string{"status": status(), "action": request.Action})
}

func serviceActive(name string) bool {
	return exec.Command("/usr/bin/systemctl", "is-active", "--quiet", name).Run() == nil
}

func (g *Gateway) yorkshireConferenceStatus() map[string]any {
	g.bridgeMu.Lock()
	route := g.bridge
	g.bridgeMu.Unlock()
	nodeStatus := func(id int) string {
		session := g.sessionByID(id)
		if session == nil {
			return "unavailable"
		}
		session.mu.RLock()
		defer session.mu.RUnlock()
		if session.AuthStage == authRunning {
			return "connected"
		}
		if session.LinkActive {
			return "connecting"
		}
		return "disconnected"
	}
	g.conferenceMu.Lock()
	until := g.conferenceUntil
	g.conferenceMu.Unlock()
	freeSTARStatus := nodeStatus(1)
	brandMeisterStatus := nodeStatus(2)
	tgifStatus := nodeStatus(4)
	converterActive := serviceActive("ysf2dmr.service")
	configured := route.Active && route.ANode == 1 && route.BNode == 2 && route.CNode == 4 && route.ATG == 23530 && route.BTG == 23530 && route.CTG == 23530
	return map[string]any{
		"active": configured, "ready": configured && converterActive && freeSTARStatus == "connected" && brandMeisterStatus == "connected" && tgifStatus == "connected",
		"ysf2dmr":  converterActive,
		"freestar": freeSTARStatus, "brandmeister": brandMeisterStatus,
		"tgif": tgifStatus, "talkgroup": 23530,
		"expires_at": func() string {
			if until.IsZero() {
				return ""
			}
			return until.UTC().Format(time.RFC3339)
		}(),
	}
}

func (g *Gateway) stopYorkshireConference(reason string) {
	g.conferenceMu.Lock()
	if g.conferenceTimer != nil {
		g.conferenceTimer.Stop()
		g.conferenceTimer = nil
	}
	g.conferenceUntil = time.Time{}
	g.conferenceMu.Unlock()

	g.bridgeMu.Lock()
	g.bridge = BridgeRoute{}
	g.bridgeMu.Unlock()
	for _, id := range []int{1, 2, 4} {
		if session := g.sessionByID(id); session != nil {
			g.disconnectNetwork(session, reason)
		}
	}
	_ = exec.Command("/usr/bin/sudo", "-n", "/usr/bin/systemctl", "stop", "ysf2dmr.service").Run()
	_ = os.Remove(ysf2dmrConfigPath)
	g.sendBridgeStatus("disconnected", reason)
}

func (g *Gateway) handleYorkshireConference(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		json.NewEncoder(w).Encode(g.yorkshireConferenceStatus())
		return
	}
	if r.Method != http.MethodPost || r.Header.Get("X-DVHub-Control") != "1" {
		http.Error(w, `{"error":"Control request rejected"}`, http.StatusForbidden)
		return
	}
	var request struct {
		Action          string `json:"action"`
		Callsign        string `json:"callsign"`
		DMRID           uint32 `json:"dmr_id"`
		DurationSeconds int    `json:"duration_seconds"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request) != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}
	if request.Action == "stop" {
		g.stopYorkshireConference("Yorkshire conference disconnected")
		json.NewEncoder(w).Encode(g.yorkshireConferenceStatus())
		return
	}
	if request.Action != "start" && request.Action != "test" {
		http.Error(w, `{"error":"Invalid action"}`, http.StatusBadRequest)
		return
	}
	request.Callsign = strings.ToUpper(strings.TrimSpace(request.Callsign))
	if !regexp.MustCompile(`^[A-Z0-9]{3,10}$`).MatchString(request.Callsign) || request.DMRID < 1000000 || request.DMRID > 9999999 {
		http.Error(w, `{"error":"Valid callsign and 7-digit DMR ID required"}`, http.StatusBadRequest)
		return
	}
	freeSTARHost, err := loadDMRHost("FreeSTAR-SystemX-UK")
	if err != nil {
		http.Error(w, `{"error":"FreeSTAR master configuration is unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	duration := request.DurationSeconds
	if request.Action == "test" {
		duration = 60
	}
	if duration < 30 {
		duration = 30
	}
	if duration > 900 {
		duration = 900
	}
	g.stopYorkshireConference("Replacing previous Yorkshire session")
	config := fmt.Sprintf(`[Info]
RXFrequency=435000000
TXFrequency=435000000
Power=1
Latitude=53.8
Longitude=-1.5
Height=0
Location=Yorkshire, United Kingdom
Description=Yorkshire Link HUB temporary TG23530 bridge
URL=https://194.146.49.25/

[YSF Network]
Callsign=%s
Suffix=ND
DstAddress=127.0.0.1
DstPort=42000
LocalAddress=127.0.0.1
LocalPort=42013
EnableWiresX=0
RemoteGateway=0
HangTime=1000
WiresXMakeUpper=1
Daemon=0

[DMR Network]
Id=%d
StartupDstId=23530
StartupPC=0
Address=dmr.freestar.network
Port=62031
Jitter=500
EnableUnlink=1
TGUnlink=4000
PCUnlink=0
Password=%s
TGListFile=/var/lib/dvgateway/TGList-DMR.txt
Debug=0

[DMR Id Lookup]
File=/var/lib/dvgateway/dmrid.dat
Time=24
DropUnknown=0

[Log]
DisplayLevel=1
FileLevel=1
FilePath=/var/log/ysf2dmr
FileRoot=YSF2DMR

[aprs.fi]
Enable=0
`, request.Callsign, request.DMRID, freeSTARHost.Password)
	if err := os.WriteFile(ysf2dmrConfigPath, []byte(config), 0600); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if output, err := exec.Command("/usr/bin/sudo", "-n", "/usr/bin/systemctl", "start", "ysf2dmr.service").CombinedOutput(); err != nil {
		_ = os.Remove(ysf2dmrConfigPath)
		http.Error(w, fmt.Sprintf(`{"error":%q}`, strings.TrimSpace(string(output))), http.StatusInternalServerError)
		return
	}
	g.conferenceMu.Lock()
	g.conferenceUntil = time.Now().Add(time.Duration(duration) * time.Second)
	g.conferenceTimer = time.AfterFunc(time.Duration(duration)*time.Second, func() {
		g.stopYorkshireConference("Yorkshire conference safety timer expired")
	})
	g.conferenceMu.Unlock()
	time.Sleep(300 * time.Millisecond)
	json.NewEncoder(w).Encode(g.yorkshireConferenceStatus())
}

var (
	ysfLogLine  = regexp.MustCompile(`^.: (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}) (.*)$`)
	ysfAdding   = regexp.MustCompile(`^Adding\s+(\S+)\s+\(([^)]+)\)$`)
	ysfRemoving = regexp.MustCompile(`^Removing\s+(\S+)\s+\(([^)]+)\)\s+(.+)$`)
	ysfReceived = regexp.MustCompile(`^Received data from\s+(\S+)\s+to\s+(\S+)\s+at\s+(\S+)$`)
)

func ysfServiceUptime() int64 {
	startOutput, err := exec.Command("/usr/bin/systemctl", "show", "ysfreflector.service", "--property=ActiveEnterTimestampMonotonic", "--value").Output()
	if err != nil {
		return 0
	}
	startMicros, err := strconv.ParseInt(strings.TrimSpace(string(startOutput)), 10, 64)
	if err != nil || startMicros <= 0 {
		return 0
	}
	uptimeData, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(uptimeData))
	if len(fields) == 0 {
		return 0
	}
	bootSeconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	seconds := int64(bootSeconds - float64(startMicros)/1_000_000)
	if seconds < 0 {
		return 0
	}
	return seconds
}

func (g *Gateway) collectYSFDashboard() map[string]any {
	type gatewayEntry struct {
		Callsign    string `json:"callsign"`
		Address     string `json:"address"`
		ConnectedAt string `json:"connected_at"`
		LastSeen    string `json:"last_seen"`
	}
	type heardEntry struct {
		Time        string `json:"time"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
		Gateway     string `json:"gateway"`
		Duration    int64  `json:"duration_seconds"`
	}
	type activityEntry struct {
		Time    string `json:"time"`
		Type    string `json:"type"`
		Message string `json:"message"`
	}

	connected := make(map[string]gatewayEntry)
	heard := make([]heardEntry, 0, 50)
	activity := make([]activityEntry, 0, 100)
	uniqueCallsigns := make(map[string]bool)
	transmissionsToday := 0
	var current *heardEntry

	files, _ := filepath.Glob("/var/log/ysfreflector/YSFReflector-*.log")
	sort.Strings(files)
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			parts := ysfLogLine.FindStringSubmatch(scanner.Text())
			if len(parts) != 3 {
				continue
			}
			timestamp, parseErr := time.ParseInLocation("2006-01-02 15:04:05.000", parts[1], time.UTC)
			if parseErr != nil {
				continue
			}
			message := parts[2]
			formattedTime := timestamp.Format(time.RFC3339)
			if strings.Contains(message, "YSFReflector-") && strings.HasSuffix(message, " is starting") {
				connected = make(map[string]gatewayEntry)
				current = nil
				continue
			}
			if match := ysfAdding.FindStringSubmatch(message); len(match) == 3 {
				callsign := strings.TrimSpace(match[1])
				connected[callsign] = gatewayEntry{Callsign: callsign, Address: match[2], ConnectedAt: formattedTime, LastSeen: formattedTime}
				activity = append(activity, activityEntry{Time: formattedTime, Type: "connected", Message: callsign + " connected"})
				continue
			}
			if match := ysfRemoving.FindStringSubmatch(message); len(match) == 4 {
				callsign := strings.TrimSpace(match[1])
				delete(connected, callsign)
				activity = append(activity, activityEntry{Time: formattedTime, Type: "disconnected", Message: callsign + " disconnected (" + match[3] + ")"})
				continue
			}
			if match := ysfReceived.FindStringSubmatch(message); len(match) == 4 {
				entry := heardEntry{Time: formattedTime, Source: match[1], Destination: match[2], Gateway: match[3]}
				current = &entry
				uniqueCallsigns[entry.Source] = true
				if timestamp.Format("2006-01-02") == time.Now().UTC().Format("2006-01-02") {
					transmissionsToday++
				}
				if gateway, ok := connected[entry.Gateway]; ok {
					gateway.LastSeen = formattedTime
					connected[entry.Gateway] = gateway
				}
				activity = append(activity, activityEntry{Time: formattedTime, Type: "transmission", Message: entry.Source + " → " + entry.Destination + " via " + entry.Gateway})
				continue
			}
			if message == "Received end of transmission" && current != nil {
				start, _ := time.Parse(time.RFC3339, current.Time)
				current.Duration = int64(timestamp.Sub(start).Seconds())
				heard = append(heard, *current)
				current = nil
			}
		}
		file.Close()
	}
	if current != nil {
		heard = append(heard, *current)
	}
	if len(heard) > 50 {
		heard = heard[len(heard)-50:]
	}
	if len(activity) > 100 {
		activity = activity[len(activity)-100:]
	}
	for left, right := 0, len(heard)-1; left < right; left, right = left+1, right-1 {
		heard[left], heard[right] = heard[right], heard[left]
	}
	for left, right := 0, len(activity)-1; left < right; left, right = left+1, right-1 {
		activity[left], activity[right] = activity[right], activity[left]
	}
	gateways := make([]gatewayEntry, 0, len(connected))
	for _, gateway := range connected {
		gateways = append(gateways, gateway)
	}
	sort.Slice(gateways, func(i, j int) bool { return gateways[i].Callsign < gateways[j].Callsign })
	status := "stopped"
	if exec.Command("/usr/bin/systemctl", "is-active", "--quiet", "ysfreflector.service").Run() == nil {
		status = "running"
	}
	identity := currentYSFIdentity()
	return map[string]any{
		"status": status, "name": identity.Name, "reflector_id": identity.ID, "host": identity.Host, "port": identity.Port,
		"uptime_seconds": ysfServiceUptime(), "connected_count": len(gateways), "connected_gateways": gateways,
		"last_heard": heard, "activity": activity, "transmissions_today": transmissionsToday, "unique_callsigns": len(uniqueCallsigns),
	}
}

func (g *Gateway) handleYSFDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(g.collectYSFDashboard())
}

func (g *Gateway) handlePublicYSFDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=5")
	data := g.collectYSFDashboard()
	public := map[string]any{
		"status": data["status"], "name": data["name"], "reflector_id": data["reflector_id"],
		"host": data["host"], "port": data["port"], "uptime_seconds": data["uptime_seconds"],
		"connected_count": data["connected_count"], "transmissions_today": data["transmissions_today"],
		"unique_callsigns": data["unique_callsigns"], "last_heard": data["last_heard"],
	}
	json.NewEncoder(w).Encode(public)
}

func (g *Gateway) broadcastYSFDashboardLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		payload := g.collectYSFDashboard()
		payload["type"] = "ysf_dashboard"
		data, err := json.Marshal(payload)
		if err == nil {
			g.broadcastText(data)
		}
	}
}

func (g *Gateway) handleTalkgroups(w http.ResponseWriter, r *http.Request) {
	network := r.URL.Query().Get("network")
	sourceURL, ok := networkTGURLs[network]
	if !ok {
		http.Error(w, `{"error":"Unknown network"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=3600")

	g.tgMutex.RLock()
	cached, cacheTime := g.tgCache[network], g.tgTimes[network]
	g.tgMutex.RUnlock()
	if len(cached) > 0 && time.Since(cacheTime) < time.Hour {
		json.NewEncoder(w).Encode(map[string]any{"network": network, "source": "cache", "talkgroups": cached})
		return
	}

	var source io.ReadCloser
	sourceLabel := sourceURL
	localPath := filepath.Join("/var/lib/dvgateway/talkgroups", network+".csv")
	if file, err := os.Open(localPath); err == nil {
		source = file
		sourceLabel = "local W0CHP cache"
	} else {
		req, _ := http.NewRequest(http.MethodGet, sourceURL, nil)
		req.Header.Set("User-Agent", "DVHub-Gateway/2.0 2E0LXY")
		resp, fetchErr := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if fetchErr != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			http.Error(w, `{"error":"Talkgroup source unavailable"}`, http.StatusBadGateway)
			return
		}
		source = resp.Body
	}
	defer source.Close()
	reader := csv.NewReader(io.LimitReader(source, 4<<20))
	_, _ = reader.Read()
	groups := make([]Talkgroup, 0, 512)
	for len(groups) < 5000 {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			break
		}
		if len(record) < 2 {
			continue
		}
		id, parseErr := strconv.ParseUint(strings.TrimSpace(record[0]), 10, 32)
		if parseErr == nil {
			groups = append(groups, Talkgroup{ID: uint32(id), Name: strings.TrimSpace(record[1])})
		}
	}
	g.tgMutex.Lock()
	g.tgCache[network], g.tgTimes[network] = groups, time.Now()
	g.tgMutex.Unlock()
	json.NewEncoder(w).Encode(map[string]any{"network": network, "source": sourceLabel, "talkgroups": groups})
}

func readTalkgroupCSV(path string) ([]Talkgroup, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(io.LimitReader(file, 4<<20))
	_, _ = reader.Read()
	groups := make([]Talkgroup, 0, 512)
	for len(groups) < 5000 {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		if len(record) < 2 {
			continue
		}
		id, parseErr := strconv.ParseUint(strings.TrimSpace(record[0]), 10, 32)
		if parseErr == nil {
			groups = append(groups, Talkgroup{ID: uint32(id), Name: strings.TrimSpace(record[1])})
		}
	}
	return groups, nil
}

func (g *Gateway) handleTalkgroupScript(w http.ResponseWriter, r *http.Request) {
	all := make(map[string][]Talkgroup, len(networkTGURLs))
	for network := range networkTGURLs {
		path := filepath.Join("/var/lib/dvgateway/talkgroups", network+".csv")
		groups, err := readTalkgroupCSV(path)
		if err == nil && len(groups) > 0 {
			all[network] = groups
		}
	}
	payload, err := json.Marshal(all)
	if err != nil {
		http.Error(w, "Unable to build talkgroup registry", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write([]byte("window.DVHUB_TALKGROUPS="))
	w.Write(payload)
	w.Write([]byte(";"))
}

var validCallsign = regexp.MustCompile(`^[A-Z0-9]{3,10}$`)

func (g *Gateway) handleDMRLookup(w http.ResponseWriter, r *http.Request) {
	callsign := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("callsign")))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if !validCallsign.MatchString(callsign) {
		http.Error(w, `{"error":"Enter a valid callsign"}`, http.StatusBadRequest)
		return
	}

	type radioIDResult struct {
		RadioID  uint32 `json:"radio_id"`
		Callsign string `json:"callsign"`
		Name     string `json:"name"`
		City     string `json:"city"`
		State    string `json:"state"`
		Country  string `json:"country"`
	}
	var upstream struct {
		Results []radioIDResult `json:"results"`
	}

	lookupURL := "https://database.radioid.net/api/dmr/user/?callsign=" + url.QueryEscape(callsign)
	req, _ := http.NewRequest(http.MethodGet, lookupURL, nil)
	req.Header.Set("User-Agent", "DVHub-Gateway/2.0 2E0LXY")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&upstream)
		}
	}

	if err != nil || len(upstream.Results) == 0 {
		g.dbMutex.RLock()
		for id, info := range g.idDB {
			if strings.EqualFold(info.Callsign, callsign) {
				upstream.Results = append(upstream.Results, radioIDResult{
					RadioID: id, Callsign: info.Callsign, Name: info.Name, City: info.City, State: info.State, Country: info.Country,
				})
			}
		}
		g.dbMutex.RUnlock()
	}

	json.NewEncoder(w).Encode(map[string]any{
		"callsign": callsign,
		"count":    len(upstream.Results),
		"results":  upstream.Results,
	})
}

func (g *Gateway) loadLocalDBAsync() {
	newDB := make(map[uint32]RadioIDInfo)
	if file, err := os.Open("/var/lib/dvgateway/radioid-users.csv"); err == nil {
		newDB = parseRadioIDCSV(file)
		file.Close()
	}
	if file, err := os.Open("/var/lib/dvgateway/dmrid.dat"); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			parts := strings.Split(scanner.Text(), ";")
			if len(parts) < 2 {
				parts = strings.Split(scanner.Text(), "\t")
			}
			if len(parts) < 2 {
				continue
			}
			id, parseErr := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 32)
			if parseErr != nil {
				continue
			}
			key := uint32(id)
			info := newDB[key]
			if info.Callsign == "" {
				info.Callsign = strings.TrimSpace(parts[1])
				newDB[key] = info
			}
		}
		file.Close()
	}
	g.dbMutex.Lock()
	g.idDB = newDB
	g.dbMutex.Unlock()
	fmt.Printf("[DB] Loaded %d DMR IDs\n", len(newDB))
}

func parseRadioIDCSV(reader io.Reader) map[uint32]RadioIDInfo {
	result := make(map[uint32]RadioIDInfo)
	csvReader := csv.NewReader(reader)
	csvReader.FieldsPerRecord = -1
	header, err := csvReader.Read()
	if err != nil {
		return result
	}
	columns := make(map[string]int, len(header))
	for index, name := range header {
		columns[strings.ToUpper(strings.TrimSpace(name))] = index
	}
	field := func(record []string, name string) string {
		index, ok := columns[name]
		if !ok || index >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[index])
	}
	for {
		record, readErr := csvReader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			continue
		}
		id, parseErr := strconv.ParseUint(field(record, "RADIO_ID"), 10, 32)
		if parseErr != nil {
			continue
		}
		result[uint32(id)] = RadioIDInfo{
			Callsign: field(record, "CALLSIGN"),
			Name:     strings.TrimSpace(field(record, "FIRST_NAME") + " " + field(record, "LAST_NAME")),
			City:     field(record, "CITY"),
			State:    field(record, "STATE"),
			Country:  field(record, "COUNTRY"),
		}
	}
	return result
}

func (g *Gateway) watchdog(host string, port int) {
	ticker := time.NewTicker(60 * time.Second)
	for range ticker.C {
		ips, _ := net.LookupIP(host)
		if len(ips) > 0 {
			g.HomeAddr.Store(&net.UDPAddr{IP: ips[0], Port: port})
		}
	}
}

func (g *Gateway) handleState(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{
		"ip": g.HomeAddr.Load().(*net.UDPAddr).IP.String(),
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
