# Yorkshire Link cross-mode converters

All production cross-mode paths use room or talkgroup `23530` where the
protocol carries a destination number.

## P25 to DMR using the remote DV30

`p252dmr-dv30.patch` replaces MMDVM_CM's ARM-only md380 AMBE vocoder with
DVHub's restricted UDP DV30 service. The VPS performs the P25 IMBE software
decode/encode using `nostar/imbe_vocoder`; the remote DV30 performs the DMR
AMBE+2 decode/encode. Only one direction is active at a time.

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
