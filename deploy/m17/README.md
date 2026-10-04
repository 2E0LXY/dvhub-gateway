# M17-YLH module A

The M17 leg uses open-source Codec2 in `USRP2M17`; it does not consume an
additional DV30 channel. `mrefd` provides reflector `M17-YLH`, module `A`.
USRP audio enters private local app_rpt node `1998`, which is permanently
linked in transceive mode to public AllStar node `530471`. The existing
AllStar-to-DMR bridge therefore carries M17 audio to conference TG23530 and
back through the same bounded AMBE conversion already used by AllStar and
EchoLink.

Build inputs are pinned:

- `n7tae/mrefd` `7ba8c9dc3de2a43f44ba05e53b010c26c4147f2f`
- `ShaYmez/MMDVM_CM` `5c0a387da2cedfa47fc171fc57c48fee252c91aa`

Install as root from a checked-out repository:

```sh
deploy/m17/install-m17.sh
```

The install does not publish M17-YLH in DVRef, Ham-DHT, or a public hostfile.
Until the operator deliberately registers it, clients must add M17-YLH with
the hub address and UDP port `17000` manually. If a host firewall is enabled,
allow inbound UDP 17000 before external clients are expected to connect.
