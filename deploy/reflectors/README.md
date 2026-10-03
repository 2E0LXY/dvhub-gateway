# Yorkshire Link P25 and NXDN reflectors

The HUB uses the `P25Reflector` and `NXDNReflector` programs from
[`nostar/DVReflectors`](https://github.com/nostar/DVReflectors), pinned to the
tested source revision recorded by the deployment.

| Mode | UDP port | Room / talkgroup |
| --- | ---: | ---: |
| P25 | 41000 | Yorkshire Link single room (23530 mapping) |
| NXDN | 41400 | 23530 |

The services run as separate unprivileged users with systemd hardening. The
daily identity timer refreshes the NXDN RadioID database and copies DVHub's DMR
ID database into the P25 reflector's private data directory.

These are native reflector endpoints. Installing them does not by itself
transcode P25 audio into the DMR/YSF conference; that requires the
corresponding protocol gateway/converter process.

## NXDN to DMR conference bridge

`NXDN2DMR.ini` and `nxdn2dmr.service` connect the local NXDN reflector on
UDP 41400 to FreeSTAR talkgroup 23530. NXDN and DMR use compatible AMBE+2
voice, so MMDVM_CM remaps the frames without a PCM transcoding generation.
The bridge logs in as hotspot suffix 02, independently of the primary
conference leg. Replace `__FREESTAR_MASTER_PASSWORD__` only on the server;
never commit that network secret.

P25 still requires a true IMBE to AMBE+2 conversion. It is intentionally
kept out of this service and uses the separate hardware-only DV30 design.
