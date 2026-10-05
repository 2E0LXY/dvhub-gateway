# XLXd and DVxCode staging

This directory builds a pinned, D-STAR-only XLXd reflector and prepares its
connection to the separately versioned DVxCode bridge. It does not enable or
start either service automatically.

## Audio path

```text
D-STAR radio/hotspot
        |
        | DPlus / DExtra / DCS
        v
XLXd module A <--- DExtra ---> dvxbridge (DVxCode) <--- HBP ---> isolated DMR test TG
```

XLXd is deliberately compiled without its native DMR and YSF listeners. The
VPS already uses UDP 62030 for the DVHub local DMR master, and duplicate
listeners would either prevent startup or create an uncontrolled loop.
DVxCode is the only component allowed to cross from the XLXd module into DMR.

## Safety gates

- XLXd is pinned to upstream commit `bf5d0148dbdf2534af129ca3cc034c5051dcfc8d`.
- The installer leaves `xlxd.service` and `dvxbridge.service` disabled.
- `--with-dvxcode` fails unless the nested `dvxcode` module, bridge command,
  deployment configuration and `VALIDATION.md` all exist.
- The initial bridge must use a test talkgroup absent from every DVHub
  `BridgeRoute`; it must not use TG23530 during validation.
- The XLX number must be checked against the current reflector directory. Do
  not enable call-home for a private shadow reflector.
- No credentials belong in Git. The installer writes only an example DVxCode
  configuration and the operator creates `/etc/dvhub/dvxbridge.ini` locally.

## Installation after the gates pass

Install the codec and bridge binaries for offline and shadow-test preparation:

```bash
sudo deploy/xlxd/install-dvxcode-stage.sh
```

This command installs the binaries, example configuration and disabled systemd
unit only. It deliberately removes any stale production-validation marker and
does not create `/etc/dvhub/dvxbridge.ini`, start a service, or send network
traffic. The dashboard control remains fail-closed until validation is recorded.

After DV30 comparison and the shadow-test configuration are ready, install the
pinned XLXd reflector:

```bash
sudo deploy/xlxd/install-xlxd.sh \
  --callsign XLXnnn \
  --listen-ip YOUR_VPS_ADDRESS \
  --with-dvxcode
```

Before starting anything:

1. Confirm `XLXnnn` is unused and appropriate for the intended public or
   private deployment.
2. Copy `/etc/dvhub/dvxbridge.ini.example` to
   `/etc/dvhub/dvxbridge.ini`, insert server-side credentials, and select an
   isolated test TG and XLXd module A.
3. Confirm UDP 30001, 30051, 20001, 10001 and 10002 are not occupied.
4. Start `xlxd.service` only, verify D-STAR linking, then start
   `dvxbridge.service` for the shadow test.
5. Run the one-week shadow test and record the results in
   `dvxcode/VALIDATION.md` before considering TG23530.

Rollback is independent of the rest of DVHub:

```bash
sudo systemctl disable --now dvxbridge.service xlxd.service
```

Stopping these services does not restart the gateway, AllStar, M17, YSF, P25
or NXDN.
