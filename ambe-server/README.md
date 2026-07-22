# DVHub DV30 AMBE server

This service exposes one genuine DVMEGA DVstick 30/ThumbDV AMBE3000 over a restricted UDP connection for DVHub Gateway. It drives the USB serial device directly in DVSI packet mode and supports both directions:

- `0x61 + channel + 320-byte PCM16LE` → `0x62 + channel + 9-byte AMBE`
- `0x63 + channel + 9-byte AMBE` → `0x64 + channel + 320-byte PCM16LE`
- `0x70` → `0x71 + JSON health information`

Requests are serialized because one DV30 is one hardware vocoder resource. Invalid source addresses receive no reply.

`AMBE_MODE=dmr` supports DVHub's current DMR/AMBE+2 path. `AMBE_MODE=dstar` prepares the stick for the older D-Star AMBE rate, but the DVHub D-Star network/framing path must also be completed before it can be used. A single stick cannot remain configured for DMR and D-Star modes simultaneously; two sticks are recommended for simultaneous cross-mode transcoding.

## Network requirement

The configured direct route is `zx3de49.glddns.com` on public UDP `2468`, forwarded to `192.168.1.131:2468`. The service source allowlist must remain `194.146.49.25/32`, and the Linux firewall should allow UDP 2468 only from that VPS. A Tailscale or WireGuard route remains the safer alternative because this compact real-time protocol deliberately has no password exchange.

The hostname used by the hub must resolve directly to the router. `zx3de49.glddns.com` currently provides that unproxied route.

## Install on the Linux machine with the DV30

```bash
ls -l /dev/serial/by-id/
cd ambe-server
sudo ./install.sh
sudoedit /etc/dvhub/ambe-server.env
sudo systemctl enable --now dvhub-ambe-server
sudo systemctl status dvhub-ambe-server
```

Use the persistent `/dev/serial/by-id/...` name. Earlier FTDI-based sticks may use 230400 baud; later CP2102 versions commonly use 460800. The service log reports the detected AMBE product and firmware after successful initialisation.

Test health from the permitted DVHub VPS:

```bash
printf '\x70' | nc -u -w1 zx3de49.glddns.com 2468
```

Configure the hub with `zx3de49.glddns.com:2468`, select one hardware vocoder, and switch the vocoder mode to hardware. If the device or route is unavailable, the hub falls back to its software codec rather than transmitting an empty frame.

## Compatibility and licence

The serial packet framing follows DVSI's AMBE-3000 packet interface. The DVMEGA reset sequence and mode constants were validated against the GPL-licensed [marrold/AMBEServer](https://github.com/marrold/AMBEServer) and [DVSwitch/Analog_Bridge](https://github.com/DVSwitch/Analog_Bridge) implementations. This directory is licensed under GPL-2.0-or-later; the rest of DVHub Gateway retains its existing licence.
