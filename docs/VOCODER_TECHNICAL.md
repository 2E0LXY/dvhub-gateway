# Hardware vocoder design

DVHub does not implement an AMBE or AMBE+2 software codec. The former experimental transform was not interoperable and has been retired.

AMBE conversion is performed only by an allowlisted DV30/DV3000 service. The gateway maintains a small fixed socket set and bounded per-endpoint concurrency. A frame that cannot obtain a hardware slot or receive a response within the real-time deadline fails closed; it is never replaced with synthetic codec bits.

The local UDP broker on `127.0.0.1:2461` exists for installed converter services. It is not an Internet API. Remote hardware should be reached over a private LAN, WireGuard or Tailscale route and restricted by `/etc/dvhub/vocoder-targets.txt`.

One device may be sufficient for a single half-duplex path. Two devices provide independent hardware capacity for opposite conversion directions. Capacity is still bounded, and the one-talker conference gate remains the primary arbitration mechanism.

No objective speech-quality or latency percentage is claimed. Quality and timing depend on the DVSI hardware, source audio, radio network, converter and network path.
