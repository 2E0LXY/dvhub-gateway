# Yorkshire Link cross-mode converters

All production cross-mode paths use room or talkgroup `23530` where the
protocol carries a destination number.

## P25 to DMR using the remote DV30

`p252dmr-dv30.patch` replaces MMDVM_CM's ARM-only md380 AMBE vocoder with
DVHub's restricted UDP DV30 service. The VPS performs the P25 IMBE software
decode/encode using `nostar/imbe_vocoder`; the remote DV30 performs the DMR
AMBE+2 decode/encode. Only one direction is active at a time.

`usrp2dmr-dv30-pipeline.patch` submits all three AMBE frames in a 60 ms DMR
burst concurrently. This keeps the converter clock moving at real time over a
WAN vocoder path instead of paying three serial network round trips per burst.

Build inputs are pinned during deployment:

- MMDVM_CM `5c0a387`
- imbe_vocoder `03423cb`

The runtime config contains a placeholder for the FreeSTAR master password.
Deployment replaces it from the existing protected server configuration; the
secret must never be committed.

## Native AllStar 530471 to DMR

The hub runs native ASL3/Asterisk node `530471`; the retired `IAX_Bridge`
client is no longer part of the deployment. Patched `USRP2DMR` exchanges audio
with ASL3 and connects to FreeSTAR TG23530 with hotspot suffix 04. Its AMBE
traffic goes to the gateway's loopback hardware broker at `127.0.0.1:2461`, so
P25 and AllStar converters share the bounded DV30/DV3000 pool. Frames fail
closed whenever the hardware pool is busy or unavailable. Network
passwords remain only in protected server-side configuration.

## M17 to the shared AllStar PCM bus

M17-YLH module A connects through `USRP2M17` to private local app_rpt node
`1998`. That node is permanently linked to public node `530471`, allowing M17,
EchoLink and AllStar to share the same PCM bus and existing USRP2DMR/DV30
conversion. See `deploy/m17/README.md` for the pinned build and installation.

Native identities cross that PCM-only hop through a loopback-only UDP metadata
side-channel (`35171/35172`). M17 callsigns are accepted only when they map to
the current DMR lookup database; unknown callsigns use the configured bridge
ID. In the reverse direction, the DMR ID is resolved to a callsign before it is
used as the M17 source. Messages are source-port checked, expire after five
seconds, and are cleared at end-of-transmission to prevent stale attribution.

## Latency profile

DMR-facing converters use a 120 ms receive jitter buffer (two 60 ms DMR
bursts). The previous 500 ms setting added avoidable delay on the low-latency
VPS-to-FreeSTAR route. Do not reduce it below 120 ms without measuring live
loss and late-frame concealment; a single-burst buffer has little tolerance
for scheduler or network jitter.
