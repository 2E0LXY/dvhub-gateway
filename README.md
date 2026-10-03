# DV Hub Gateway v2.0

**Professional Web-to-RF Gateway for DMR and YSF Digital Voice Networks**

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-1.21+-00ADD8.svg)
![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-lightgrey.svg)
[![Android](https://github.com/2E0LXY/dvhub-gateway/actions/workflows/android.yml/badge.svg)](https://github.com/2E0LXY/dvhub-gateway/actions/workflows/android.yml)

A control and monitoring gateway for DMR, YSF and cross-mode digital-voice services. AMBE encode/decode requires an allowlisted DV30/DV3000 hardware service. Browser-native DMR/YSF transmission is deliberately disabled until the native framers implement the full published wire formats; live cross-mode voice uses the MMDVM gateway services.

## 🎯 Features

### Core Capabilities
- **Hardware Vocoder Pool**: One or two allowlisted DV30/DV3000 services, with hardware failover and no synthetic AMBE fallback
- **Remote DV30 Server**: Secure-overlay support for a USB DVstick 30 on another Linux machine, with hardware TX encoding and RX decoding
- **Multi-Protocol Control**: DMR, YSF, P25, NXDN and AllStar service monitoring/control
- **Web Interface**: Modern cyberpunk-themed dashboard with real-time traffic monitoring
- **Fail-closed audio**: unavailable hardware returns an error rather than injecting non-AMBE bits

### Technical Highlights
- WebSocket binary transport for PCM audio
- AudioWorklet-based browser DSP (48kHz↔8kHz conversion)
- Dead-man's switch (1.5s timeout)
- TG/Reflector change interlock (800ms teardown)
- Real-time traffic logging with DMR ID lookup

## 📋 Table of Contents

- [Quick Start](#quick-start)
- [Installation](#installation)
- [Configuration](#configuration)
- [Usage](#usage)
- [Architecture](#architecture)
- [API Reference](#api-reference)
- [Troubleshooting](#troubleshooting)
- [Contributing](#contributing)
- [License](#license)

## 🚀 Quick Start

```bash
# Clone repository
git clone https://github.com/2E0LXY/dvhub-gateway.git
cd dvhub-gateway

# Build
go build -o dvhub-gateway gateway.go

# Run
./dvhub-gateway
```

Access dashboard at `http://localhost:8080`

### Android remote

The native Android controller is in [`android/`](android/). It provides secure gateway control, live status and activity, network/talkgroup selection, YSF management, bridge-matrix controls, DV30/DV3000 configuration, speaker RX and press-and-hold microphone TX.

**[Download the latest Yorkshire Link HUB APK](https://github.com/2E0LXY/dvhub-gateway/releases)**

Build it with:

```bash
cd android
./gradlew assembleDebug
```

The local APK is produced at `android/app/build/outputs/apk/debug/app-debug.apk`. Generated build output is not committed to Git; installable builds are published under [GitHub Releases](https://github.com/2E0LXY/dvhub-gateway/releases), and GitHub Actions uploads an artifact for each Android build.

### Remote DV30 / AMBE server

The self-contained Linux service and systemd installer are in [`ambe-server/`](ambe-server/). Run it beside the USB DV30 and connect it to the public gateway using the configured UDP route or a VPN. It supports hardware AMBE encode and decode and restricts requests to the configured gateway address.

## 📦 Installation

### Prerequisites
- Go 1.21 or higher
- Modern web browser (Chrome/Firefox/Safari)
- Microphone access for TX

### From Source

```bash
# Install Go dependencies
go mod download

# Build binary
go build -o dvhub-gateway gateway.go

# Optional: Install systemd service
sudo cp deploy/dvhub-gateway.service /etc/systemd/system/
sudo install -d -o root -g dvhub -m 0750 /etc/dvhub
sudo install -o root -g dvhub -m 0640 deploy/gateway.json.example /etc/dvhub/gateway.json
sudo install -o root -g dvhub -m 0640 deploy/vocoder-targets.example /etc/dvhub/vocoder-targets.txt
sudo install -o root -g root -m 0440 deploy/dvhub-gateway.sudoers /etc/sudoers.d/dvhub-gateway
sudo visudo -cf /etc/sudoers.d/dvhub-gateway
# Edit both /etc/dvhub files for this deployment before starting.
sudo systemctl enable dvhub-gateway
sudo systemctl start dvhub-gateway
```

### Directory Structure
```
/var/lib/dvgateway/     # Database files (DMR IDs, YSF hosts)
/var/www/dvhub/         # Static web files
/usr/local/bin/         # Gateway binary
```

## ⚙️ Configuration

### Radio Parameters

Configure your callsign and DMR ID in browser localStorage:

```javascript
// Open browser console (F12) and run:
localStorage.setItem('dv_hub_callsign', 'M0ABC');
localStorage.setItem('dv_hub_dmrid', '2350000');
localStorage.setItem('dv_hub_tx_freq', '430.2000');
localStorage.setItem('dv_hub_rx_freq', '430.2000');
```

### Network Configuration

BrandMeister API v2 credentials are stored only on the gateway at `/etc/dvhub/brandmeister-api.token` with `root:dvhub` ownership and mode `0640`. The dashboard reports only whether the credential is configured and verified; the JWT is never returned to a browser, written to logs, or committed to Git. This API credential is separate from the BrandMeister hotspot-security password used by the DMR master protocol.

### Permanent Yorkshire TG23530 conference

The gateway can supervise a permanent, bidirectional YSF 23530 ↔ FreeSTAR ↔ BrandMeister/TGIF conference. Its credentials live only in `/etc/dvhub/yorkshire-conference.json`; use `root:dvhub` ownership and mode `0640`. Required JSON fields are `enabled`, `callsign`, `ysf_dmr_id`, `bridge_dmr_id`, `bridge_essid`, `brandmeister_password`, and `tgif_password`. Never commit the live file.

When enabled, the supervisor restores the YSF2DMR service, the three DMR logins, and the protected one-talker bridge route after restarts. **Disconnect / Pause** writes `/var/lib/dvgateway/yorkshire-conference.paused`, preventing automatic reconnection until **Connect permanently** is selected. Rejected credentials are retried no more than once every five minutes.

The FreeSTAR System X leg sends `TS2_1=23530;` in its protocol-options login, booking only TG23530 as the static simplex talkgroup. The bridge independently checks every received frame's destination, so traffic for any other talkgroup is discarded even if a master sends it unexpectedly.

The dashboard and Android app use registered DMR ID `2351633` on session/node 7 with ESSID `02` for manual FreeSTAR operation. Selecting a talkgroup automatically sends `TS2_1=<selected TG>;` on that separate login. The permanent conference remains isolated on node 1 using DMR ID `2344399`, ESSID `01`, and TG23530.

Built-in DMR network DNS targets are configured in `dmrNetworkTargets` in `gateway.go`; master passwords and port overrides come from `/var/lib/dvgateway/DMR_Hosts.txt`. Site-specific public identity and vocoder addresses belong in `/etc/dvhub/gateway.json` and `/etc/dvhub/vocoder-targets.txt`; examples are under `deploy/`.

```go
dmrNetworkTargets := map[string]string{
    "FreeSTAR-SystemX-UK":  "dmr.freestar.network:62031",
    "BrandMeister-UK-2341": "2341.master.brandmeister.network:62031",
    "DMRPlus-FreeSTAR":     "ipsc2.freestar.network:62031",
    "TGIF":                 "tgif.network:62031",
    "FreeDMR-UK":           "hotspot.uk.freedmr.link:62031",
}
```

### Reverse Proxy (Caddy)

```caddy
Use the repository `Caddyfile`, set `DVHUB_PASSWORD_HASH`, and keep the Go listener bound to `127.0.0.1:8080`. Caddy authenticates protected routes and overwrites the trusted authenticated-user header before proxying.
```

Auto HTTPS via Let's Encrypt - no certificates needed!

## 📖 Usage

### Dashboard Tab

**Read-only monitoring interface:**
- Hardware Information (CPU, platform, kernel)
- Service Status (gateway, network, audio, vocoder, TX)
- Live Activity Log (callsigns, talkgroups, timestamps)
- Network Status (connection state, active TG/REF)
- Radio Information (frequencies, callsign, DMR ID)

### Administration Tab

**All configuration controls:**

1. **Gateway Control**
   - Time-Out Timer (60s - 180s or disabled)
   - DV30 Hardware Vocoder (IP:Port)
   - Vocoder Mode Toggle (SW/HW)

2. **Network Configuration**
   - Separate live-status card and indicator light for every configured network
   - Green/amber/red/grey connection states with the active target TG
   - Conference-managed FreeSTAR, BrandMeister and TGIF legs are visibly protected from accidental manual retuning
   - Target Network Selection
   - Talkgroup/Reflector Input
   - Password (for BrandMeister/FreeDMR)
   - Connect/Disconnect Button

3. **Audio Pipeline Analytics**
   - Worklet statistics
   - Buffer depth monitoring
   - Quality metrics

### Basic Workflow

1. **Configure Radio Info** (localStorage in browser console)
2. **Go to Administration Tab**
3. **Select Network** (DMR - FreeStar UK / BrandMeister / YSF)
4. **Enter Talkgroup** (e.g., TG 235 for UK Wide)
5. **Click "CONNECT LINK"**
6. **Grant Microphone Permission** (browser prompt)
7. **Press PTT Button** (or SPACEBAR) to transmit

### PTT Controls

- **Mouse**: Click and hold PTT button
- **Keyboard**: Press and hold SPACEBAR
- **Touch**: Touch and hold PTT button (mobile)

**Safety Features:**
- Time-Out Timer (configurable 60-180s)
- Dead-Man's Switch (server-side 1.5s)
- Auto-disconnect on tab close

## 🏗️ Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Browser (Client)                          │
│  ┌──────────────┐  ┌───────────────┐  ┌──────────────┐     │
│  │ Microphone   │→│  AudioWorklet  │→│  WebSocket   │     │
│  │ 48kHz PCM    │  │  Decimate 8kHz │  │  Binary      │     │
│  └──────────────┘  └───────────────┘  └──────────────┘     │
└─────────────────────────────────────────────────────────────┘
                            ↓ WebSocket (320 bytes/20ms)
┌─────────────────────────────────────────────────────────────┐
│                 Gateway (Go Server)                          │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  Vocoder Selection                                    │  │
│  │  ┌───────────────────┐    ┌──────────────────────┐  │  │
│  │  │ Software AMBE+2   │    │ Hardware DV30/AMBE   │  │  │
│  │  │ Pure Go Codec     │    │ UDP Client           │  │  │
│  │  └───────────────────┘    └──────────────────────┘  │  │
│  └──────────────────────────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  Protocol Framers                                     │  │
│  │  ┌──────────────┐        ┌──────────────────────┐   │  │
│  │  │ DMR Framer   │        │ YSF Framer           │   │  │
│  │  │ 55 bytes     │        │ 120 bytes            │   │  │
│  │  └──────────────┘        └──────────────────────┘   │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
                            ↓ UDP Packets
              ┌─────────────────────────────────┐
              │   DMR/YSF Network Servers       │
              │   - BrandMeister                │
              │   - FreeStar                    │
              │   - FreeDMR                     │
              │   - YSF Reflectors              │
              └─────────────────────────────────┘
```

### Voice pipeline

The local UDP broker accepts PCM/AMBE requests only when allowlisted AMBE hardware is available. It returns an error when every hardware device is busy or offline. The legacy private DCT transform in the source is not AMBE+2 and is not used by live network or broker paths.

## 🔧 API Reference

### AMBE Link Heartbeat

`GET /api/vocoder/health` performs a live UDP probe of the configured DV30 server. It reports link state, round-trip time, hardware-active state, device product and firmware, uptime, encode/decode totals, and errors. The configured address is intentionally omitted from the response.

### WebSocket Commands (JSON)

#### Connect to Network
```json
{
  "cmd": "node_state",
  "node_id": 1,
  "active": true,
  "mode": "DMR",
  "target": "BrandMeister",
  "tg": 235,
  "password": "your_password"
}
```

#### Start Transmission
```json
{
  "cmd": "tx_start",
  "node_id": 1
}
```

#### Stop Transmission
```json
{
  "cmd": "tx_stop"
}
```

#### Set Vocoder Mode
```json
{
  "cmd": "set_vocoder",
  "type": "hybrid"
}
```

#### Configure DV30
```json
{
  "cmd": "set_dv30",
  "count": 2,
  "addr1": "dv30-a.example.net:2468",
  "addr2": "dv30-b.example.net:2468"
}
```

Both exact endpoints must first be listed by the server administrator in `/etc/dvhub/vocoder-targets.txt`.

### Binary Messages

**TX Audio (Browser → Server)**
- Format: Raw PCM Int16LE
- Size: 320 bytes (160 samples @ 8kHz)
- Frequency: 50 Hz (every 20ms)

**RX Audio (Server → Browser)**
- Format: Raw PCM Int16LE
- Size: 320 bytes (160 samples @ 8kHz)
- Decoded from AMBE frames

### Server Events (JSON)

#### Traffic Event
```json
{
  "type": "traffic",
  "data": {
    "time": "15:42:13",
    "node": 1,
    "mode": "DMR",
    "raw_id": 2350123,
    "callsign": "M0ABC",
    "target": "TG 235"
  }
}
```

#### State Sync
```json
{
  "type": "state_sync",
  "node_id": 1,
  "active": false
}
```

## 🐛 Troubleshooting

### WebSocket Connection Failed

**Symptoms**: Red WebSocket indicator, no connection

**Solutions**:
1. Check gateway is running: `systemctl status dvhub-gateway`
2. Check port 8080 is accessible: `netstat -tlnp | grep 8080`
3. Check browser console for errors (F12)
4. Verify firewall allows port 8080

### No Audio RX

**Symptoms**: Connected but no received audio

**Solutions**:
1. Check UDP listener: `netstat -ulnp | grep 62031`
2. Verify network selection matches actual network
3. Check browser console for AudioWorklet errors
4. Ensure speakers/headphones connected

### Microphone Permission Denied

**Symptoms**: Cannot connect, audio error

**Solutions**:
1. Click lock icon in browser address bar
2. Set microphone permission to "Allow"
3. Refresh page (F5)
4. For HTTPS: Ensure valid certificate

### DV30 Hardware Vocoder Not Working

**Symptoms**: Connected but poor audio quality

**Solutions**:
1. Test the allowlisted DV30 server health: `printf '\x70' | nc -u -w1 dv30.example.net 2468`
2. Verify IP:Port in Administration tab
3. Check DV30 server is running
4. Ensure network route to DV30 server
5. Check firewall allows UDP to DV30 port

### High Latency

**Symptoms**: Delayed audio, choppy playback

**Solutions**:
1. Close other applications using CPU
2. Use wired network connection
3. Reduce browser tab count
4. Check CPU load in dashboard (<10% normal)
5. Ensure 48kHz audio sample rate supported

### Console Logging

Enable debug logging in browser console:
```javascript
localStorage.setItem('debug', 'true');
```

Log prefixes:
- `[WS]` - WebSocket events
- `[AUDIO]` - Audio pipeline
- `[PTT]` - Transmit control
- `[TG]` - Talkgroup changes

## 📊 Performance

Performance and perceived audio quality depend on the selected network, host load, WAN path and AMBE hardware. No PESQ or percentage-quality claim is made without a reproducible benchmark and published samples.

## 🤝 Contributing

Contributions welcome! Please:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to branch (`git push origin feature/amazing`)
5. Open a Pull Request

### Code Style
- Go: `gofmt` formatting
- JavaScript: 2-space indentation
- Comments: Explain *why*, not *what*

### Testing
```bash
# Run Go tests
go test ./...

# Build and test
go build -o dvhub-gateway gateway.go
./dvhub-gateway
```

## 📝 License

MIT License - see [LICENSE](LICENSE) file for details.

## 👥 Authors

- **2E0LXY** - Initial work and primary maintainer

## 🙏 Acknowledgments

- **DVSI** - AMBE codec specification
- **ETSI** - DMR TS 102 361 standard
- **mbelib** - Open source AMBE reference implementation
- **Pi-Star** - Dashboard design inspiration
- **Amateur Radio Community** - Testing and feedback

## 📚 Documentation

- [User Manual](docs/USER_MANUAL.md) - Complete user guide
- [Technical Documentation](VOCODER_TECHNICAL.md) - Vocoder implementation details
- [API Reference](docs/API.md) - WebSocket protocol
- [Deployment Guide](docs/DEPLOYMENT.md) - Production setup

## 🔗 Links

- **Homepage**: https://github.com/2E0LXY/dvhub-gateway
- **Issues**: https://github.com/2E0LXY/dvhub-gateway/issues
- **Discussions**: https://github.com/2E0LXY/dvhub-gateway/discussions

## ⚠️ Disclaimer

This software is for amateur radio use only. Ensure you have appropriate licenses and permissions before transmitting on any frequency or network.

---

**Built with ❤️ by the Amateur Radio Community**

**73 de 2E0LXY**
