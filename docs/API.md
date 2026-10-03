# DVHub gateway API

Caddy protects all control endpoints and `/ws` with Basic Authentication. State-changing HTTP requests also require `X-DVHub-Control: 1`. The only anonymous API is the public, read-only YSF dashboard feed.

The Go service listens on `/run/dvhub/gateway.sock` on Linux. Clients must use the HTTPS reverse proxy; forged identity headers sent by unrelated local processes cannot reach the socket unless that process has membership of the `dvhub` group.

## WebSocket

Connect to `/ws` on the configured HTTPS origin. Supported text commands include DMR `node_state`, matrix `bridge_state`, hardware-only `set_vocoder` and allowlisted `set_dv30` configuration.

```json
{"cmd":"node_state","node_id":2,"active":true,"mode":"DMR","target":"BrandMeister-UK-2341","tg":23530,"callsign":"M0ABC","dmr_id":2350000,"repeater_id":235000000,"password":"session-only credential","options":""}
```

Only `mode: "DMR"` is accepted for generic sessions. Native YSF sessions, binary browser audio and `tx_start` are denied. Use the managed reflector/converter services for voice.

```json
{"cmd":"set_dv30","count":1,"addr1":"100.64.0.10:2468","addr2":""}
{"cmd":"set_vocoder","type":"hw"}
```

Vocoder targets must appear in `/etc/dvhub/vocoder-targets.txt`; `sw` and `hybrid` are rejected.

## HTTP endpoints

- `GET /api/system` — live host CPU, memory and temperature information.
- `GET /api/state` — service, network-session and capability state.
- `GET /api/vocoder/health` — hardware-vocoder link and error counters.
- `GET /api/talkgroups?network=...` — selected-network talkgroups.
- `GET /api/dmr_lookup?callsign=...` — matching RadioID registrations.
- `GET /api/ysf_dashboard` — authenticated reflector status.
- `GET /api/public/ysf_dashboard` — intentionally public, read-only YSF status.
- `POST /api/ysf_control` — start, stop or restart the reflector.
- `GET|POST /api/yorkshire_conference` — inspect or control TG23530 conference.
- `GET|POST /api/allstar_config` — inspect or control the AllStar node.

Passwords and API tokens are accepted only for the relevant control operation and are never returned by a status endpoint.
