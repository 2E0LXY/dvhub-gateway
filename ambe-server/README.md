# DV30 / DV3000 hardware service

This service exposes a USB DV30/DV3000 to DVHub converter processes. It performs hardware AMBE encode/decode only; it has no software-codec fallback.

## Security boundary

The compact UDP protocol has no user authentication or encryption. Do not publish it directly to the Internet. Put the gateway and this host on the same private LAN, WireGuard network or Tailscale network, and set `AMBE_ALLOW` to the gateway's exact private `/32` address. Keep the firewall closed to every other source.

The public hostname, router forwarding details and deployment IPs are intentionally not documented or committed. Store those in the operator's private deployment notes.

## Install

1. Connect the DV30/DV3000 USB device to the Linux host.
2. Copy `ambe-server.env.example` to `/etc/dvhub/ambe-server.env` and set the serial device, private bind address/port and exact private gateway allowlist.
3. Run the included installer as root.
4. Add the service's private `host:port` to `/etc/dvhub/vocoder-targets.txt` on the gateway.
5. Verify `/api/vocoder/health` through the authenticated dashboard.

Use one hardware device for a single half-duplex conversion path. A second device adds bounded capacity and can keep opposite conversion directions independent. A busy, unreachable or late device causes the frame to fail closed.

## Health probe

From an allowed private host, send the protocol health byte to the private endpoint:

```bash
printf '\\x70' | nc -u -w1 100.64.0.10 2468
```

Replace the example address with the private address assigned to your DV30 host.
