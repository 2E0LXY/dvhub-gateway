# DVHub gateway API

The HTTPS reverse proxy protects all control endpoints and `/ws` with Basic
Authentication. State-changing HTTP requests also require
`X-DVHub-Control: 1`. The public YSF status feed is the only anonymous API.

## WebSocket

Connect to `wss://ai.2e0lxy.uk/ws`. JSON text frames control network sessions;
binary frames carry signed 16-bit little-endian mono PCM. Every audio frame is
exactly 320 bytes: 160 samples at 8 kHz, or 20 milliseconds.

### Network session

```json
{"cmd":"node_state","node_id":2,"active":true,"mode":"DMR","target":"BrandMeister-UK-2341","tg":23530,"callsign":"2E0LXY","dmr_id":2344399,"repeater_id":234439900,"password":"session-only credential","options":""}
```

The server answers with `network_status` events containing `node_id`, `state`,
`message`, `network`, `dmr_id` and `repeater_id`. Clients must wait for state
`connected`; successfully sending the command does not mean the master login
was accepted.

### TX lease

Send `{"cmd":"tx_start","node_id":2}` before binary audio and
`{"cmd":"tx_stop"}` when PTT is released. Only one authenticated, RadioID-
validated identity may hold the transmitter at a time. `tx_status` reports
`active`, `busy`, `denied` or `idle`.

### Matrix bridge

```json
{"cmd":"bridge_state","active":true,"a_node":7,"a_tg":23530,"b_node":2,"b_tg":23530,"c_node":6,"c_tg":23530}
```

Set `active` to `false` to disconnect the current route. The third leg is
optional.

### Vocoder configuration

```json
{"cmd":"set_dv30","count":2,"addr1":"zx3de49.glddns.com:2468","addr2":"192.168.1.132:2468"}
{"cmd":"set_vocoder","type":"hybrid"}
```

Modes are `sw`, `hw` and `hybrid`. Hybrid distributes work across both
configured devices and immediately uses the software codec when all hardware
slots are busy or unavailable. The loopback UDP broker on `127.0.0.1:2461`
provides the same pool to P25 and AllStar converters.

## HTTP endpoints

- `GET /api/system` — live host CPU, memory and temperature information.
- `GET /api/state` — gateway and network-session state.
- `GET /api/vocoder/health` — vocoder link and fallback counters.
- `GET /api/talkgroups?network=...` — talkgroups for a selected network.
- `GET /api/dmr_lookup?callsign=...` — matching RadioID identities.
- `GET /api/ysf_dashboard` — authenticated YSF reflector status.
- `GET /api/public/ysf_dashboard` — intentionally public read-only YSF status.
- `POST /api/ysf_control` — start, stop or restart the reflector.
- `GET|POST /api/yorkshire_conference` — inspect or control TG23530 conference.
- `GET|POST /api/allstar_config` — inspect or control native ASL3 node 530471.

Passwords and API tokens are accepted only where required for a control action
and are never returned by status endpoints.
