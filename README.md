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
- Authenticated WebSocket control transport
- Managed MMDVM-family services for all live voice paths
- Single-talker conference arbitration
- TG change interlock (800ms teardown)
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

The native Android controller is in [`android/`](android/). It provides secure gateway control, live status and activity, network/talkgroup selection, YSF management, bridge-matrix controls and DV30/DV3000 configuration. Native app audio and PTT are disabled; voice uses the managed gateway services.

**[Download the latest Yorkshire Link HUB APK](https://github.com/2E0LXY/dvhub-gateway/releases)**

Build it with:

```bash
cd android
./gradlew assembleDebug
```

The local APK is produced at `android/app/build/outputs/apk/debug/app-debug.apk`. Generated build output is not committed to Git; installable builds are published under [GitHub Releases](https://github.com/2E0LXY/dvhub-gateway/releases), and GitHub Actions uploads an artifact for each Android build.

### Remote DV30 / AMBE server

The self-contained Linux service and systemd installer are in [`ambe-server/`](ambe-server/). Run it beside the USB DV30 and connect it over a private LAN, WireGuard or Tailscale route. It supports hardware AMBE encode and decode and restricts requests to the configured gateway address.

## 📦 Installation

### Prerequisites
- Go 1.21 or higher
- Modern web browser (Chrome/Firefox/Safari)

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
sudo install -o root -g root -m 0755 deploy/dvhub-set-ysf-identity /usr/local/sbin/dvhub-set-ysf-identity
sudo install -o root -g root -m 0440 deploy/dvhub-gateway.sudoers /etc/sudoers.d/dvhub-gateway
sudo visudo -cf /etc/sudoers.d/dvhub-gateway
sudo usermod -aG dvhub caddy
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

The dashboard and Android app accept an optional registered DMR ID for session/node 7 with ESSID `02` for manual FreeSTAR operation; when blank they use the main configured DMR ID. Selecting a talkgroup automatically sends `TS2_1=<selected TG>;` on that separate login. The permanent conference remains isolated on node 1 using its protected server-side identity and TG23530.

### EchoLink through AllStar 530471

The authenticated web dashboard and Android app can configure a previously
validated EchoLink `-L` or `-R` account on ASL3 node `530471`. The EchoLink
password is sent only in the protected request, staged in a mode-0600 file and
removed after the root helper updates Asterisk. It is never committed to Git or
returned by the status API. EchoLink audio joins the existing ASL3 PCM bus, so
it reuses the AllStar-to-DMR hardware conversion instead of consuming a second
DV30 stream. Allow inbound UDP `5198-5199` in the VPS firewall before expecting
incoming EchoLink audio.

P25-to-ASL and M17-to-ASL remain planned integrations. They are not enabled by
configuration alone: each needs a standards-compliant P25/M17-to-USRP component
and end-to-end half-duplex testing before it can replace the current P25 bridge
or join the live conference. D-Star stays disabled until a second dependable
AMBE channel is available. The md380 software codec is test/standby only and is
never selected automatically during a live transmission.

### Passive media-quality monitoring

The dashboard health matrix reads operating-system and media counters without
placing a synthetic stream in the voice path. It reports five-second host RX
drops, five-minute DMR sequence loss and p95 arrival jitter, plus real DV30
media deadline latency and failures. `NO TRAFFIC` is intentionally neutral,
not green. DV30 identity probes are cached by one server-side worker every 15
seconds and are suspended while codec media is active, so opening additional
dashboards cannot compete with live vocoder frames.

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
Use the repository `Caddyfile`, set `DVHUB_PASSWORD_HASH`, and add the `caddy` service account to the `dvhub` group. On Linux the Go service listens only on `/run/dvhub/gateway.sock`; Caddy authenticates protected routes and proxies through that group-restricted socket.
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
   - DV30/DV3000 hardware endpoint and topology
   - Hardware-only, fail-closed vocoder status

2. **Network Configuration**
   - Separate live-status card and indicator light for every configured network
   - Green/amber/red/grey connection states with the active target TG
   - Conference-managed FreeSTAR, BrandMeister and TGIF legs are visibly protected from accidental manual retuning
   - Target Network Selection
   - Talkgroup/Reflector Input
   - Password (for BrandMeister/FreeDMR)
   - Connect/Disconnect Button

3. **Audio service status**
   - Managed converter-service health
   - Hardware-vocoder heartbeat and counters
   - Explicit native-audio capability state

### Basic Workflow

1. **Configure your registered callsign and DMR ID**
2. **Go to Administration Tab**
3. **Select a DMR network**
4. **Enter Talkgroup** (e.g., TG 235 for UK Wide)
5. **Click "CONNECT LINK"**
6. **Use the managed radio/reflector services for live voice**

Browser and Android PTT/RX are disabled. The dashboard is a control and
monitoring surface; it never constructs native DMR/YSF voice frames.

## 🏗️ Architecture

```
Browser / Android
  └─ authenticated HTTPS + WebSocket control
       └─ Caddy
            └─ /run/dvhub/gateway.sock
                 ├─ dashboard, identities, status and service control
                 ├─ YSFReflector / YSF2DMR
                 ├─ P25 / NXDN / AllStar converters
                 └─ bounded DV30/DV3000 hardware broker
```

### Voice pipeline

The local UDP broker accepts PCM/AMBE requests only when allowlisted AMBE hardware is available. It returns an error when every hardware device is busy, late or offline. There is no private software codec or browser-native protocol framer in the runtime.

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

`tx_start`, `tx_stop` and binary audio are rejected. Live voice uses the
managed MMDVM-family services.

#### Set Vocoder Mode
```json
{
  "cmd": "set_vocoder",
  "type": "hw"
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

### Binary messages

Binary browser audio is deliberately unsupported and receives a `tx_status`
denial. This prevents non-interoperable voice data from reaching live networks.

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
2. Check `/run/dvhub/gateway.sock` exists and Caddy belongs to the `dvhub` group
3. Check browser console for errors (F12)
4. Validate Caddy and confirm the HTTPS route is reachable

### No browser/app audio

This is expected. Native browser/app TX and RX are disabled. Check the
appropriate YSFReflector, YSF2DMR, P25, NXDN or AllStar converter service for
live voice faults.

### DV30 Hardware Vocoder Not Working

**Symptoms**: Hardware heartbeat offline or a converter reports frame errors

**Solutions**:
1. Test the allowlisted DV30 server health: `printf '\x70' | nc -u -w1 dv30.example.net 2468`
2. Verify IP:Port in Administration tab
3. Check DV30 server is running
4. Ensure the private LAN/VPN route to the DV30 server is available
5. Check the exact private gateway address is allowlisted

### High Latency

**Symptoms**: Delayed audio, choppy playback

**Solutions**:
1. Keep the hardware vocoder on the LAN or a low-latency private VPN
2. Use a wired connection where practical
3. Check host load and converter-service logs
4. Confirm hardware replies fit inside the 40 ms frame deadline

### Console Logging

Enable debug logging in browser console:
```javascript
localStorage.setItem('debug', 'true');
```

Log prefixes:
- `[WS]` - WebSocket events
- `[VOC]` - Hardware vocoder and broker events
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

### CI-gated VPS updates

Production can follow `main` automatically without storing radio credentials in
Git. The deploy timer requires successful Go and Android checks, builds and
validates in staging, installs only the gateway binary, dashboards, Caddyfile
and gateway unit, and rolls back if the local socket health check fails.

See [deploy/gitops/README.md](deploy/gitops/README.md). Reflector, cross-mode,
AllStar and conference services are preserved and are never restarted by the
automatic update.

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
