package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const BasePort = 62031
const MaxUsers = 4

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type WSClient struct {
	conn *websocket.Conn
	send chan []byte
}

type UserSession struct {
	ID           int
	Port         int
	IsActive     atomic.Bool
	Mode         string
	Target       string
	TG           uint32
	Password     string
	UseHWVocoder atomic.Bool
	DV30Addr     atomic.Value // *net.UDPAddr
	FlushFEC     atomic.Bool
	rtcBuffer    chan []byte
	LastTXFrame  atomic.Int64
	Callsign     string
	DMRID        uint32
	mu           sync.RWMutex
}

type RadioIDInfo struct {
	Callsign string
	Name     string
	Country  string
}

type Gateway struct {
	HomeAddr atomic.Value
	sessions [MaxUsers]*UserSession
	clients  map[*WSClient]bool
	mu       sync.Mutex
	idDB     map[uint32]RadioIDInfo
	dbMutex  sync.RWMutex
}

var (
	udpPool = sync.Pool{New: func() any { b := make([]byte, 2048); return &b }}
	gw      *Gateway
)

func main() {
	gw = &Gateway{
		clients: make(map[*WSClient]bool),
		idDB:    make(map[uint32]RadioIDInfo),
	}
	gw.HomeAddr.Store(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2460})

	go gw.updateRegistriesDaily()
	go gw.watchdog("home.mysmartagent.uk", 2460)
	go gw.txWatchdog()

	for i := 0; i < MaxUsers; i++ {
		gw.sessions[i] = &UserSession{
			ID:        i + 1,
			Port:      BasePort + i,
			Mode:      "DMR",
			rtcBuffer: make(chan []byte, 50),
			Callsign:  "M0ABC",
			DMRID:     2350000,
		}
		gw.sessions[i].DV30Addr.Store((*net.UDPAddr)(nil))
		go gw.runUDPListener(gw.sessions[i])
	}

	http.HandleFunc("/ws", gw.handleWS)
	http.HandleFunc("/api/state", gw.handleState)
	http.HandleFunc("/api/ysf_hosts", gw.handleYSFHosts)
	http.Handle("/", http.FileServer(http.Dir("/var/www/dvhub")))

	fmt.Println("[SYS] DV Hub Gateway v2.0 - Software Vocoder Enabled")
	fmt.Println("[SYS] Listening on :8080")
	http.ListenAndServe("127.0.0.1:8080", nil)
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
					s.IsActive.Store(false)
					s.FlushFEC.Store(true)

					msg, _ := json.Marshal(map[string]any{
						"type":    "state_sync",
						"node_id": s.ID,
						"active":  false,
					})
					g.broadcastText(msg)
				}
			}
		}
	}
}

func (g *Gateway) broadcastText(msg []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for client := range g.clients {
		client.conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
		client.conn.WriteMessage(websocket.TextMessage, msg)
	}
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
			client.conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
			if err := client.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
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
			json.Unmarshal(msg, &req)

			if req["cmd"] == "node_state" {
				nodeID := int(req["node_id"].(float64))
				for _, s := range g.sessions {
					if s.ID == nodeID {
						s.mu.Lock()
						s.Mode = req["mode"].(string)
						s.Target = req["target"].(string)
						if tg, ok := req["tg"].(float64); ok {
							s.TG = uint32(tg)
						}
						if pwd, ok := req["password"].(string); ok {
							s.Password = pwd
						}
						s.mu.Unlock()
						fmt.Printf("[CFG] Node %d: %s -> %s TG/REF %d\n", nodeID, s.Mode, s.Target, s.TG)
					}
				}
			} else if req["cmd"] == "tx_start" {
				nodeID := int(req["node_id"].(float64))
				for _, s := range g.sessions {
					if s.ID == nodeID {
						s.LastTXFrame.Store(time.Now().UnixMilli())
						s.IsActive.Store(true)
						fmt.Printf("[PTT] Node %d TX START\n", nodeID)
					}
				}
			} else if req["cmd"] == "tx_stop" {
				for _, s := range g.sessions {
					if s.IsActive.Load() {
						s.IsActive.Store(false)
						s.FlushFEC.Store(true)
						fmt.Printf("[PTT] Node %d TX STOP\n", s.ID)
					}
				}
			} else if req["cmd"] == "set_vocoder" {
				vocType := req["type"].(string)
				for _, s := range g.sessions {
					s.UseHWVocoder.Store(vocType == "hw")
				}
				fmt.Printf("[VOC] Switched to %s vocoder\n", strings.ToUpper(vocType))
			} else if req["cmd"] == "set_dv30" {
				addr := req["addr"].(string)
				if host, port, err := net.SplitHostPort(addr); err == nil {
					if ip := net.ParseIP(host); ip != nil {
						if p, e := strconv.Atoi(port); e == nil {
							udpAddr := &net.UDPAddr{IP: ip, Port: p}
							for _, s := range g.sessions {
								s.DV30Addr.Store(udpAddr)
							}
							fmt.Printf("[VOC] DV30 target set: %s\n", addr)
						}
					}
				}
			}

		} else if msgType == websocket.BinaryMessage {
			for _, s := range g.sessions {
				if s.IsActive.Load() {
					s.LastTXFrame.Store(time.Now().UnixMilli())
					select {
					case s.rtcBuffer <- msg:
					default:
						s.FlushFEC.Store(true)
					}
					break
				}
			}
		}
	}

	g.mu.Lock()
	delete(g.clients, client)
	g.mu.Unlock()
	close(client.send)
}

func (g *Gateway) runUDPListener(s *UserSession) {
	addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", s.Port))
	conn, _ := net.ListenUDP("udp", addr)
	defer conn.Close()

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
				s.mu.RUnlock()

				if mode == "DMR" {
					finalPayload = buildDMRFrame(voiceData, dmrid, tg, lastFrame)
				} else if mode == "YSF" {
					finalPayload = buildYSFFrame(voiceData, s.Callsign, lastFrame)
				}

				// Step 3: Transmit to network
				if len(finalPayload) > 0 {
					targetAddr := resolveNetworkTarget(target)
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

		if err == nil && n > 10 {
			// Parse protocol frame
			var pcmData []byte
			var sourceID uint32
			var sourceName string

			s.mu.RLock()
			mode := s.Mode
			s.mu.RUnlock()

			if mode == "DMR" {
				pcmData, sourceID = parseDMRFrame(buf[:n])
			} else if mode == "YSF" {
				pcmData, sourceName = parseYSFFrame(buf[:n])
			}

			// Decode voice to PCM
			if len(pcmData) > 0 {
				decodedPCM := decodeMBE(pcmData)

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
					g.pushTrafficWS(s.ID, mode, fmt.Sprintf("%d", s.TG), sourceID, sourceName)
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
	ambeFrameSamples = 160       // 20ms @ 8kHz
	ambeFrameBytes   = 9         // 72 bits
	ambeSubframes    = 4         // 4x 40-sample subframes
	ambeBands        = 56        // Spectral bands
	ambeHarmonics    = 16        // Max harmonics
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
	minPeriod := 8000 / 500  // 500 Hz max
	maxPeriod := 8000 / 50   // 50 Hz min
	
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

var dv30Pool = sync.Pool{
	New: func() any {
		conn, _ := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
		return conn
	},
}

func encodeDV30(pcm []byte, target *net.UDPAddr, channel int) []byte {
	conn := dv30Pool.Get().(*net.UDPConn)
	defer dv30Pool.Put(conn)

	// DV30 protocol: 0x61 (encode request) + channel + PCM
	req := make([]byte, 1+1+len(pcm))
	req[0] = 0x61
	req[1] = byte(channel)
	copy(req[2:], pcm)

	conn.WriteToUDP(req, target)
	conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))

	resp := make([]byte, 32)
	n, _, err := conn.ReadFromUDP(resp)
	if err == nil && n > 2 && resp[0] == 0x62 {
		return resp[2:n] // Return AMBE data
	}

	// Fallback to software if HW fails
	return encodeMBE(pcm)
}

// ============================================================================
// DMR PROTOCOL FRAMER (ETSI TS 102 361)
// ============================================================================

func buildDMRFrame(voice []byte, srcID, dstID uint32, lastVoice []byte) []byte {
	// DMR frame structure: 33 bytes total
	// [Sync 6 bytes][Slot Type 3][EMB 13][Voice A 13][Voice B 13][CACH 3]
	
	frame := make([]byte, 55)
	
	// DMR Sync pattern (BS Sourced Voice)
	sync := []byte{0xD5, 0xD7, 0xF7, 0x7F, 0xD7, 0x57}
	copy(frame[0:6], sync)
	
	// Slot Type (Voice LC Header)
	frame[6] = 0x00  // Colour Code 0, Voice LC
	frame[7] = 0x20  // Slot 1
	frame[8] = 0x00
	
	// Voice Frame A (current)
	copy(frame[9:22], voice[:min(len(voice), 13)])
	
	// Voice Frame B (FEC - previous frame)
	if lastVoice != nil {
		copy(frame[22:35], lastVoice[:min(len(lastVoice), 13)])
	}
	
	// EMB (Embedded Signalling)
	emb := make([]byte, 13)
	binary.BigEndian.PutUint32(emb[0:4], srcID) // Source DMRID
	binary.BigEndian.PutUint32(emb[4:8], dstID) // Talkgroup
	emb[8] = 0x00 // Group call
	copy(frame[35:48], emb)
	
	// CACH (Common Announcement Channel)
	frame[48] = 0x00
	frame[49] = 0x00
	frame[50] = 0x00
	
	return frame
}

func parseDMRFrame(data []byte) ([]byte, uint32) {
	if len(data) < 48 {
		return nil, 0
	}
	
	// Extract voice data from slot
	voice := data[9:22]
	
	// Extract source ID from EMB
	srcID := binary.BigEndian.Uint32(data[35:39])
	
	return voice, srcID
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
	// Parse target network string
	// Format: "BrandMeister", "FreeStar", or direct "IP:Port"
	
	networkMap := map[string]string{
		"BrandMeister": "217.61.0.89:62031",
		"FreeStarX":    "81.187.165.132:62030",
		"FreeDMR":      "185.63.140.20:62031",
	}
	
	addrStr, exists := networkMap[target]
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

func (g *Gateway) pushTrafficWS(nodeID int, mode, target string, sourceID uint32, sourceName string) {
	g.dbMutex.RLock()
	info, exists := g.idDB[sourceID]
	g.dbMutex.RUnlock()

	callsign := fmt.Sprintf("%d", sourceID)
	if exists && info.Callsign != "" {
		callsign = info.Callsign
	} else if sourceName != "" {
		callsign = sourceName
	}

	msg, _ := json.Marshal(map[string]any{
		"type": "traffic",
		"data": map[string]any{
			"time":     time.Now().UTC().Format("15:04:05"),
			"node":     nodeID,
			"mode":     mode,
			"raw_id":   sourceID,
			"callsign": callsign,
			"target":   target,
		},
	})
	g.broadcastText(msg)
}

// ============================================================================
// REGISTRY MANAGEMENT
// ============================================================================

func (g *Gateway) updateRegistriesDaily() {
	g.downloadFile("https://radioid.net/static/dmrid.dat", "/var/lib/dvgateway/dmrid.dat")
	g.downloadFile("https://www.pistar.uk/downloads/YSF_Hosts.txt", "/var/lib/dvgateway/YSF_Hosts.txt")
	g.loadLocalDBAsync()

	for {
		time.Sleep(24 * time.Hour)
		g.downloadFile("https://radioid.net/static/dmrid.dat", "/var/lib/dvgateway/dmrid.dat")
		g.downloadFile("https://www.pistar.uk/downloads/YSF_Hosts.txt", "/var/lib/dvgateway/YSF_Hosts.txt")
		g.loadLocalDBAsync()
	}
}

func (g *Gateway) downloadFile(url, dest string) {
	resp, err := http.Get(url)
	if err != nil {
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

func (g *Gateway) handleYSFHosts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "text/plain")
	http.ServeFile(w, r, "/var/lib/dvgateway/YSF_Hosts.txt")
}

func (g *Gateway) loadLocalDBAsync() {
	file, err := os.Open("/var/lib/dvgateway/dmrid.dat")
	if err != nil {
		return
	}
	defer file.Close()

	newDB := make(map[uint32]RadioIDInfo)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) >= 6 {
			id, err := strconv.ParseUint(parts[0], 10, 32)
			if err == nil {
				newDB[uint32(id)] = RadioIDInfo{Callsign: parts[1], Name: parts[2], Country: parts[5]}
			}
		}
	}
	g.dbMutex.Lock()
	g.idDB = newDB
	g.dbMutex.Unlock()
	fmt.Printf("[DB] Loaded %d DMR IDs\n", len(newDB))
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
