# DV Hub Gateway - Complete User Manual

## Table of Contents

1. [Introduction](#1-introduction)
2. [Getting Started](#2-getting-started)
3. [Dashboard Interface](#3-dashboard-interface)
4. [Administration Panel](#4-administration-panel)
5. [Operating Procedures](#5-operating-procedures)
6. [Advanced Features](#6-advanced-features)
7. [Troubleshooting](#7-troubleshooting)
8. [FAQ](#8-faq)

---

## 1. Introduction

### What is DV Hub Gateway?

DV Hub Gateway is a professional web-to-RF gateway that allows you to connect your web browser directly to DMR and YSF digital voice networks. No additional hardware required beyond a computer with a microphone.

### Key Benefits

- **No Special Hardware**: Works with any computer with a microphone
- **Multiple Networks**: Supports DMR (BrandMeister, FreeStar, FreeDMR) and YSF
- **High Quality Audio**: 85-90% quality with software vocoder, 100% with hardware
- **Easy to Use**: Modern web interface, no configuration files
- **Real-Time Monitoring**: Live activity log with callsign lookup

### System Requirements

**Minimum:**
- Modern web browser (Chrome 90+, Firefox 88+, Safari 14+)
- 2 GHz dual-core processor
- 4 GB RAM
- Broadband internet connection (1 Mbps upload minimum)
- Microphone (built-in or USB)

**Recommended:**
- Chrome or Firefox latest version
- 3 GHz quad-core processor
- 8 GB RAM
- Wired ethernet connection
- External USB microphone or headset

---

## 2. Getting Started

### First Time Setup

#### Step 1: Configure Your Radio Information

Before using the gateway, you need to configure your callsign and DMR ID.

1. Open the gateway in your web browser: `http://localhost:8080`
2. Press **F12** to open browser console
3. Enter the following commands:

```javascript
localStorage.setItem('dv_hub_callsign', 'YOUR_CALLSIGN');
localStorage.setItem('dv_hub_dmrid', 'YOUR_DMR_ID');
```

**Example:**
```javascript
localStorage.setItem('dv_hub_callsign', 'M0ABC');
localStorage.setItem('dv_hub_dmrid', '2350123');
```

4. Close the console and refresh the page (F5)

#### Step 2: Verify Configuration

1. Look at the **Dashboard** tab
2. Check the **Radio Info** section (bottom right)
3. Verify your callsign and DMR ID are displayed correctly

#### Step 3: First Connection

1. Click the **Administration** tab
2. Under **Network Configuration**:
   - Select network: **DMR - FreeStar UK**
   - Enter talkgroup: **9** (Echo Test)
3. Click **CONNECT LINK** button
4. When prompted, allow microphone access
5. Wait for status to show **CONNECTED** (green)

#### Step 4: Test Transmission

1. Click and hold the **PTT** button (or press SPACEBAR)
2. Speak into your microphone: "Testing, testing, 1-2-3"
3. Release the PTT button
4. Wait 2-3 seconds - you should hear your echo back

**Congratulations!** You're now on the air.

---

## 3. Dashboard Interface

The Dashboard tab provides real-time monitoring of gateway status and network activity.

### 3.1 Hardware Information

**Location:** Top left panel

**Displays:**
- **Hostname**: Your computer's hostname
- **Kernel**: Browser version (proxy for system info)
- **Platform**: Operating system and architecture
- **CPU Load**: JavaScript heap usage (updated every 5 seconds)
- **CPU Temp**: Not available from browser (shows `-`)

**What it means:**
- CPU Load <10%: Normal operation
- CPU Load 10-30%: Moderate usage, acceptable
- CPU Load >30%: High usage, may cause audio issues

### 3.2 Service Status

**Location:** Top right panel

**Indicators:**
- **DVGateway Service**: Always shows RUNNING (web interface active)
- **Network Link**: DISCONNECTED (grey) / CONNECTED (green)
- **Audio Pipeline**: IDLE (grey) / ACTIVE (green) / ERROR (red)
- **Vocoder Engine**: SOFTWARE (green) / HARDWARE (green)
- **TX State**: IDLE (grey) / TRANSMIT (green)

**Status Colors:**
- **Green**: Active/OK
- **Grey**: Inactive/Idle
- **Red**: Error/Problem
- **Yellow**: Warning

### 3.3 Gateway Activity

**Location:** Center panel (large)

**Real-time traffic log showing:**
- **Time (EST)**: When transmission occurred
- **Mode**: DMR or YSF
- **Callsign**: Station transmitting (looked up from DMR ID database)
- **Target**: Talkgroup or Reflector
- **Src**: Timeslot (DMR only)
- **Dur(s)**: Duration in seconds
- **Loss**: Packet loss percentage
- **BER**: Bit Error Rate

**Features:**
- Most recent transmission highlighted (green background)
- Scrollable (up to 50 entries)
- Auto-updates in real-time
- UTC timestamp shown in header

### 3.4 Network Status

**Location:** Bottom left panel

**Information displayed:**
- **Connection State**: Current connection status
- **Active Network**: Which server you're connected to
- **Protocol**: DMR or YSF
- **Target TG/REF**: Currently selected talkgroup or reflector
- **Last Activity**: Time of last received transmission

**This is read-only** - to change settings, use the Administration tab.

### 3.5 Radio Info

**Location:** Bottom right panel

**Your radio configuration:**
- **TX Frequency**: Your transmit frequency
- **RX Frequency**: Your receive frequency  
- **Callsign**: Your amateur radio callsign
- **DMR ID**: Your DMR radio ID
- **Mode**: Current operating mode (DMR/YSF)

**To change these values**, see [First Time Setup](#step-1-configure-your-radio-information).

---

## 4. Administration Panel

The Administration tab contains all configuration and control functions.

### 4.1 Gateway Control Parameters

#### Time-Out Timer (ToT)

**Purpose**: Limits continuous transmission time to prevent accidental "stuck" PTT.

**Options:**
- 60 Seconds
- 90 Seconds (default)
- 120 Seconds
- 180 Seconds
- Disabled (uses server-side 1.5s DMS only)

**Recommendation**: Keep at 90 seconds for normal use.

**What happens when triggered:**
- Transmission automatically stops
- PTT button shows "TIMEOUT (TOT)" in red
- Must wait 3 seconds before transmitting again

#### DV30 Hardware Vocoder

**Purpose**: Connect to external AMBE hardware for reference-quality audio.

**Configuration:**
1. Enter IP:Port of DV30 server (e.g., `192.168.1.100:2460`)
2. Switch to **HARDWARE** vocoder mode
3. Gateway will use DV30 for encoding/decoding

**When to use:**
- You have ThumbDV or AMBE3000 hardware
- You need 100% reference quality audio
- Software vocoder quality not acceptable

**Leave blank** if using software vocoder only.

#### Vocoder Mode

**SOFTWARE (Default):**
- Pure Go AMBE+2 implementation
- 85-90% quality (PESQ 3.2-3.5)
- No external dependencies
- ~3% CPU usage
- Recommended for most users

**HARDWARE:**
- Uses DV30 server via UDP
- 100% reference quality
- Requires DV30 address configured
- <1% CPU usage
- Falls back to software if DV30 unavailable

**Switching:** Click SOFTWARE or HARDWARE button - changes take effect immediately.

### 4.2 Audio Pipeline Analytics

**Real-time audio statistics:**

- **Module**: dv-processor (AudioWorklet)
- **TX Path**: 48kHz → 8kHz decimation (browser → server)
- **RX Path**: 8kHz → 48kHz interpolation (server → browser)
- **Buffer**: RX queue depth (0-20 frames, 400ms max)
- **Vocoder**: AMBE+2 codec type
- **Quality**: Audio quality percentage
- **Latency**: End-to-end latency (TX/RX)

**Normal values:**
- Buffer: 0-5 frames (good)
- Buffer: 6-15 frames (acceptable)
- Buffer: 16-20 frames (high latency, may cause audio breakup)

**If buffer stays at 20:**
- Network congestion
- CPU overload
- Close other applications

### 4.3 Network Configuration

#### Target Network

**Available Networks:**

**DMR Networks:**
1. **DMR - FreeStar UK**
   - Open access, no password
   - Recommended for beginners
   - Default TG: 9 (Echo Test)

2. **DMR - BrandMeister**
   - Requires hotspot password
   - Worldwide coverage
   - Default TG: 235 (UK Wide)

3. **DMR - FreeDMR UK**
   - Requires hotspot password  
   - UK-focused network
   - Default TG: 2350 (UK Calling)

**YSF Networks:**
4. **YSF - Network Reflector**
   - Open access
   - Default REF: 0 (Disconnect)

#### Talkgroup / Reflector

**DMR Talkgroups:**
- **9**: Local Echo Test
- **91**: Worldwide
- **235**: UK Wide
- **2350**: UK Calling
- **4000**: Disconnect/Unlink

**YSF Reflectors:**
- **00000**: Disconnect/Unlink
- **23526**: GB HubNet
- **98765**: Parrot/Echo Test

**Changing Talkgroup:**
1. Enter new TG number
2. Click **SET** button
3. Wait 800ms (automatic disconnect → reconnect sequence)
4. Monitor activity log for confirmation

#### Network Password

**When required:**
- BrandMeister: Your hotspot password
- FreeDMR: Your hotspot password

**Not required:**
- FreeStar
- YSF Reflectors

**Finding your password:**
- BrandMeister: https://brandmeister.network → Self-Care → Security
- FreeDMR: Contact network administrator

#### Connect Link Button

**CONNECT LINK (Green):**
- Click to establish connection
- Browser will request microphone permission (allow)
- Button changes to red "DISCONNECT LINK"
- PTT button becomes active

**DISCONNECT LINK (Red):**
- Click to terminate connection
- All transmissions stopped
- PTT button becomes disabled

---

## 5. Operating Procedures

### 5.1 Making a DMR Contact

#### Preparation

1. Go to **Administration** tab
2. Select: **DMR - FreeStar UK**
3. Enter talkgroup (e.g., **235** for UK Wide)
4. Click **CONNECT LINK**
5. Return to **Dashboard** tab to monitor

#### Listening

1. Watch the **Gateway Activity** log
2. When someone transmits:
   - New row appears (highlighted green)
   - Shows callsign, TG, timestamp
   - Audio plays through speakers

#### Transmitting

1. **Listen first** - ensure TG is clear
2. Click and hold **PTT** button (or hold SPACEBAR)
3. Wait 500ms for system to key up
4. Speak clearly into microphone:
   - "This is [YOUR CALLSIGN]"
   - Your message
   - "Over"
5. Release PTT
6. Wait for response

**Best Practices:**
- Keep transmissions under 30 seconds
- Pause between words
- Use phonetics for callsigns
- Identify at start and end

#### Ending QSO

1. Final transmission: "[CALLSIGN] clear"
2. Go to **Administration** tab
3. Click **DISCONNECT LINK**
4. Or change to TG 4000 (Disconnect)

### 5.2 Making a YSF Contact

#### Preparation

1. Go to **Administration** tab
2. Select: **YSF - Network Reflector**
3. Enter reflector (e.g., **23526** for GB HubNet)
4. Click **CONNECT LINK**

#### Operating

YSF procedure similar to DMR:
- Listen on activity log
- PTT to transmit
- Identify with callsign

**YSF Differences:**
- No talkgroups, use reflectors
- Reflector 00000 = disconnect
- Single channel (no timeslots)

### 5.3 Using Echo Test

**Purpose**: Test your audio without bothering others.

#### DMR Echo Test

1. Connect to TG 9 (Local/Echo Test)
2. Key PTT and speak: "Testing 1-2-3"
3. Release PTT
4. Wait 2-3 seconds
5. Your audio plays back

**Good audio indicators:**
- Clear speech
- No distortion
- Consistent volume
- No dropouts

#### YSF Echo Test (Parrot)

1. Connect to REF 98765
2. Same procedure as DMR
3. Immediate playback

### 5.4 Emergency Stop

**If PTT gets stuck:**

1. Click **PTT button** to release
2. Or press **ESC** key
3. Or close browser tab

**Server-side protection:**
- Dead-Man's Switch triggers at 1.5s of silence
- Time-Out Timer limits total TX time

---

## 6. Advanced Features

### 6.1 Hardware Vocoder Setup

#### Equipment Needed

- ThumbDV dongle or AMBE3000 chip
- DV30 server software running
- Network connection to server

#### Configuration

1. **Start DV30 Server:**
```bash
# Example (actual command varies by software)
./dv30server -p 2460
```

2. **Configure Gateway:**
   - Administration tab
   - DV30 Hardware Vocoder: `SERVER_IP:2460`
   - Example: `192.168.1.100:2460`

3. **Switch to Hardware:**
   - Click **HARDWARE** button
   - Vocoder mode changes immediately
   - Check Service Status shows "HARDWARE"

4. **Test:**
   - Connect to TG 9
   - Transmit test message
   - Listen to quality

**Fallback:** If DV30 unreachable, automatically uses software vocoder.

### 6.2 Custom Frequencies

**Set custom frequencies** for display (cosmetic only):

```javascript
localStorage.setItem('dv_hub_tx_freq', '433.4500');
localStorage.setItem('dv_hub_rx_freq', '433.4500');
```

Refresh page (F5) to see changes.

### 6.3 Keyboard Shortcuts

| Key | Action |
|-----|--------|
| **SPACEBAR** | PTT (Push-to-Talk) |
| **ESC** | Release PTT / Panic stop |
| **F5** | Refresh page |
| **F12** | Open browser console |

**Note:** Shortcuts only work when not typing in input fields.

### 6.4 Browser Console Commands

**Enable debug logging:**
```javascript
localStorage.setItem('debug', 'true');
```

**View current configuration:**
```javascript
console.log('Callsign:', localStorage.getItem('dv_hub_callsign'));
console.log('DMR ID:', localStorage.getItem('dv_hub_dmrid'));
console.log('Vocoder:', localStorage.getItem('dv_hub_voc'));
```

**Reset all settings:**
```javascript
localStorage.clear();
location.reload();
```

---

## 7. Troubleshooting

### 7.1 Connection Issues

#### WebSocket Red/Grey

**Symptom**: Red or grey WebSocket indicator in header

**Causes & Solutions:**

1. **Gateway not running**
   ```bash
   systemctl status dvhub-gateway
   # If not running:
   systemctl start dvhub-gateway
   ```

2. **Firewall blocking port 8080**
   ```bash
   sudo ufw allow 8080/tcp
   ```

3. **Wrong URL**
   - Check address bar: should be `http://localhost:8080`
   - Or `http://SERVER_IP:8080`

4. **Browser cache**
   - Hard refresh: Ctrl+Shift+R (Ctrl+Cmd+R on Mac)

### 7.2 Audio Issues

#### No Microphone

**Symptom**: Audio indicator red, cannot connect

**Solutions:**

1. **Grant permission**
   - Click lock icon in address bar
   - Microphone: Allow
   - Refresh page

2. **Check microphone**
   - Windows: Settings → Privacy → Microphone
   - Mac: System Preferences → Security → Microphone
   - Ensure browser has permission

3. **Wrong microphone selected**
   - Check browser settings
   - Select correct microphone device

#### No Received Audio

**Symptom**: Connected, see activity, but no sound

**Solutions:**

1. **Check speakers/headphones**
   - Volume not muted
   - Correct output device selected
   - Speakers powered on

2. **Browser audio**
   - Check tab not muted (right-click tab)
   - Check browser volume mixer (Windows)

3. **Audio pipeline error**
   - Check Service Status → Audio Pipeline
   - If ERROR: disconnect and reconnect

#### Choppy/Distorted Audio

**Symptom**: Audio breaking up, robotic sound

**Solutions:**

1. **High CPU usage**
   - Close other applications
   - Check Dashboard → CPU Load
   - Goal: <10%

2. **Network issues**
   - Check internet speed
   - Use wired ethernet if possible
   - Close bandwidth-heavy apps

3. **Buffer overflow**
   - Admin tab → Audio Pipeline
   - Buffer depth >15: network congestion
   - Contact ISP if persistent

### 7.3 Transmission Issues

#### Cannot Key PTT

**Symptom**: PTT button stays disabled

**Causes & Solutions:**

1. **Not connected**
   - Admin tab → CONNECT LINK

2. **No microphone**
   - Grant browser permission
   - See [No Microphone](#no-microphone)

3. **Another user transmitting**
   - Wait for transmission to end
   - Check activity log

#### PTT Stuck

**Symptom**: Cannot release PTT

**Solutions:**

1. Click PTT button
2. Press ESC key  
3. Close browser tab
4. Server DMS will timeout at 1.5s

#### No Response to Transmissions

**Symptom**: Can TX but nobody hears

**Checks:**

1. **Verify connection**
   - Dashboard → Network Status: CONNECTED?
   - Correct network selected?

2. **Check talkgroup**
   - Admin tab → verify TG number
   - Try TG 9 (echo test)

3. **Microphone level**
   - Speak loudly enough
   - Check Windows mic level
   - Adjust input gain

---

## 8. FAQ

### General Questions

**Q: Do I need special hardware?**  
A: No. Just a computer with microphone and internet.

**Q: Is this legal?**  
A: Yes, if you hold an appropriate amateur radio license for the networks you use.

**Q: Can I use this on a phone/tablet?**  
A: Yes, modern mobile browsers are supported. PTT via touch.

**Q: Does it work on Raspberry Pi?**  
A: Yes, both as server and client (browser).

### Technical Questions

**Q: What codec is used?**  
A: AMBE+2 (software implementation) or hardware via DV30.

**Q: Can I use this for P25 or NXDN?**  
A: Not currently. DMR and YSF only.

**Q: What's the audio quality?**  
A: 85-90% with software, 100% with hardware vocoder.

**Q: Is there encryption?**  
A: No. All traffic is unencrypted (standard amateur radio practice).

### Network Questions

**Q: Which network is best?**  
A: Start with FreeStar (no password). BrandMeister has most users.

**Q: How do I get a DMR ID?**  
A: Register at https://radioid.net

**Q: Can I connect to multiple networks?**  
A: One at a time. Use Administration tab to switch.

**Q: What's a talkgroup?**  
A: Like a channel. Different groups for different topics/regions.

### Usage Questions

**Q: How do I find out who's on?**  
A: Watch the Activity Log. BrandMeister also has live dashboard at brandmeister.network

**Q: Can I talk to YSF from DMR?**  
A: Only if the network has a crosslink. Otherwise, no.

**Q: What's the maximum range?**  
A: Worldwide via internet. No RF propagation limits.

**Q: Can I use this mobile?**  
A: Yes, works on 4G/5G mobile data.

### Troubleshooting Questions

**Q: Why is CPU at 100%?**  
A: Close other apps. Check for browser extensions. Update browser.

**Q: Audio is delayed by 5+ seconds?**  
A: Network latency. Check internet connection. Use wired ethernet.

**Q: Gateway crashes when I transmit?**  
A: Check logs: `journalctl -u dvhub-gateway -n 100`. Report bug on GitHub.

**Q: Can't hear myself on echo test?**  
A: Wait full 3 seconds. Ensure TG 9. Check speaker volume.

---

## Additional Resources

- **GitHub**: https://github.com/2E0LXY/dvhub-gateway
- **Issues**: https://github.com/2E0LXY/dvhub-gateway/issues
- **Discussions**: https://github.com/2E0LXY/dvhub-gateway/discussions

## Support

For technical support:
1. Check this manual first
2. Search GitHub Issues
3. Open new issue with:
   - Browser console logs (F12)
   - Gateway logs (`journalctl -u dvhub-gateway`)
   - Steps to reproduce

**73 de 2E0LXY**
