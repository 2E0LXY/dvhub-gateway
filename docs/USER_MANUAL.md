# DVHub operator manual

DVHub is a secured control and monitoring surface for the Yorkshire Link digital-voice services. The dashboard and Android app do **not** transmit or play network audio directly. Browser/app PTT, native DMR framing and native YSF sessions are disabled because those paths are not wire-compatible with real networks.

Live voice is handled by the installed MMDVM-family services (YSFReflector, YSF2DMR and the configured P25/NXDN/AllStar converters). AMBE conversion requires an allowlisted DV30/DV3000 hardware endpoint. If hardware is unavailable or busy, the affected frame is rejected; there is no software AMBE fallback.

## Sign in

Open the HTTPS address and enter the gateway Basic Auth username and password. The public YSF status page is intentionally read-only; every control page, API and WebSocket requires authentication.

## Dashboard

The overview reports service health, current network connections, conference state, activity, hardware-vocoder heartbeat and connected YSF gateways. A red network state identifies the failing network rather than treating all links as one connection.

The YSF view reports the reflector identity, uptime, gateway address/callsign metadata and recent activity. Reflector start, stop and restart controls are authenticated administrative actions.

## Manual DMR connection

1. Open **Networks & Cross-Mode Control**.
2. Enter your callsign and registered DMR ID. Callsign lookup can return more than one registration; select the correct record.
3. For the manual FreeSTAR session, optionally enter a separate registered DMR ID. If blank, the main DMR ID is used. ESSID 02 keeps it separate from the permanent conference login.
4. Select a network and talkgroup. The talkgroup selector is populated from the selected network's data.
5. Enter a user/hotspot credential only when the selected network requires one, then connect.

Network credentials are session-only in the client. Permanent conference secrets and service API tokens remain in protected files on the server and are never returned by status APIs.

## Yorkshire Link conference

The conference supervisor can connect YSF reflector 23530 to selected DMR legs on TG23530. A single-talker gate prevents simultaneous sources from transmitting into the bridge. Use **Disconnect / Pause** before maintenance; permanent mode otherwise restores configured services after restart.

## EchoLink

EchoLink is attached to AllStar node 530471 and therefore shares the existing
ASL3 PCM-to-DMR conversion. In the dashboard or Android app, enter a callsign
that EchoLink has already validated with a `-L` or `-R` suffix, its assigned
EchoLink node number, password, registered email, display name and location.
The password is cleared from the client after submission and is never shown by
the status endpoint. The VPS firewall must allow inbound UDP 5198 and 5199.

## Hardware vocoder

Configure one or two DV30/DV3000 endpoints present in `/etc/dvhub/vocoder-targets.txt`. Use a private WireGuard/Tailscale address or a LAN address; do not expose the compact UDP broker directly to the Internet. The health display shows reachability and hardware counters.

The frame deadline is intentionally short. A distant endpoint with sustained latency above a 20 ms audio frame cannot provide real-time conversion; keep the device close to the converter services.

## Android app

The Android app provides the same authenticated monitoring and service controls. Browser-style microphone PTT and speaker RX are disabled. Configure the main DMR ID and optional manual FreeSTAR DMR ID in Settings.

## Troubleshooting

- **401 / login prompt:** check the Caddy Basic Auth environment and password hash.
- **Network rejected:** verify the DMR/repeater ID, selected master, hotspot/user password and network self-care settings.
- **Vocoder offline:** verify the allowlist, private route, UDP port and USB service on the DV30 host.
- **YSF activity empty:** confirm YSFReflector and its managed bridge are running; generic native WebSocket YSF mode is not supported.
- **Controls return 403:** access the API through Caddy. Direct access to the private Unix socket is intentionally unavailable to ordinary local processes.

Only licensed operators may transmit, and they remain responsible for network rules and their transmitted identity.
