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
transcode P25 or NXDN audio into the DMR/YSF conference; that requires the
corresponding protocol gateway/converter processes.
