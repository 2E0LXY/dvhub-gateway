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

## AllStar 530470 to DMR

`IAX_Bridge` runs as a non-registering IAX client so it cannot replace the
live AllStar registration for node 530470. It exchanges PCM audio with the
patched `USRP2DMR`, which uses the same remote DV30 hardware protocol as the
P25 bridge and logs into FreeSTAR TG23530 with hotspot suffix 04. The AllStar
FreeSTAR master password is substituted only on the server. The IAX client
password is entered through the authenticated dashboard, expanded from
`IAX_Bridge.ini` as a template, and written to the protected runtime path
`/var/lib/iax-bridge/IAX_Bridge.ini`. The API never returns the password.
