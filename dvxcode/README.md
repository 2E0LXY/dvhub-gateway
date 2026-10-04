# dvxcode

Parametric AMBE transcoder: D-STAR (AMBE 3600x2400) ⇄ DMR family (AMBE+2 3600x2450:
DMR, YSF V/D, NXDN, P25 Ph2). Pure Go, no cgo, no DVSI hardware.

Never resynthesises PCM: source bits → MBE parameters → closed-loop re-quantisation.
Removes the tandem re-estimation that causes warble.

## Status

| Phase | Scope | State |
|---|---|---|
| 1 | Frame I/O, FEC, dequantisation, capture tools | Done |
| 2 | Closed-loop quantiser (both codecs) | Done |
| 3 | Mapper, concealment, anti-warble | Done |
| 4 | Benchmark vs tandem, WAV output | Done (objective metrics; no ViSQOL) |
| 5 | `dvxbridge`: HomeBrew DMR ⇄ DExtra D-STAR with metadata | Done (loopback-tested; not yet on air) |

## Layout

| Path | Purpose |
|---|---|
| `internal/golay` | Golay(23,12), extended (24,12): correct 3, detect 4 |
| `ambe` | Interleave, PN, FEC; 9 bytes ⇄ 49 bits; DMR burst extract/insert |
| `mbe` | Decoder, closed-loop `Encoder`, `Synth`, `Analyse` (tandem baseline/test material) |
| `xcode` | `Transcoder`: concealment, pitch repair, hysteresis |
| `cmd/dvxcode` | Stream transcoder (stdin → stdout, raw or hex) |
| `cmd/dvxbridge` | Network bridge daemon (config: `deploy/dvxbridge.ini`) |
| `dmr` | DMR layer 2: full LC (RS+BPTC), embedded LC, EMB, slot type, sync, talker alias, GPS LC |
| `dstar` | Header + CRC, slow data (text, header, DPRS/NMEA), DExtra client |
| `hbp` | HomeBrew peer client (RPTL/RPTK/RPTC/RPTO, ping, DMRD) |
| `ids` | DMR ID ⇄ callsign database (DMRIds.dat / radioid.net CSV) |
| `bridge` | Call state machines, metadata translation, pacing, loss concealment |
| `cmd/mbedump` | pcap/raw/hex → CSV parameters + stability stats |
| `cmd/xcbench` | Parametric vs tandem benchmark, BER sweep, multi-hop, WAVs |
| `tools/dstardemod` | D-STAR GMSK demodulator for scanner recordings (FEC-aided timing) |
| `tools/gentables`, `reference` | Codebook generation and mbelib golden vectors |

## Usage

    dvxcode -from dstar -to dmr < in.ambe > out.ambe     # 9-byte frames, network order
    dvxcode -from dmr -to dstar -hex -stats < frames.hex
    go run ./cmd/xcbench -out bench_out                    # results + WAVs

Latency: 20 ms (lookahead) by default; `-low-latency` removes it.

| Cost (x86-64, one core) | Per 20 ms frame | Streams per core (theoretical) |
|---|---|---|
| Decode (dequantise) | 0.25 µs | — |
| Encode (closed-loop quantise) | 17 µs | — |
| Transcode end-to-end | 21–23 µs | ≈850 |

Zero heap allocations on the encode/decode path.

## Metadata translation (dvxbridge)

| D-STAR | DMR | Notes |
|---|---|---|
| MY callsign | Source ID | DMR ID database; unknown calls use `fallbackid` |
| MY + suffix + text message | Talker alias (7-bit, ≤31 chars) | e.g. `M0ABC /ID51 Hello` |
| DPRS (`$$CRC…`) or NMEA `$GPRMC/$GPGGA` | GPS info LC (embedded) | |
| — | Voice LC header, embedded voice LC, terminator | Group call to configured TG/slot |
| Header MY = callsign from DB, suffix `DMR` | Source ID | Unknown IDs: gateway callsign, text `DMR ID nnnnnnn` |
| Text message (20 chars) | Talker alias (or DB name) | |
| DPRS line `CALL>API51,DSTAR*:!lat/lon[DMR` | GPS info LC | |
| Header copy in slow data | — | Late-entry receivers recover the header |

Embedded LC rotation per superframe: voice LC → talker alias header/blocks → GPS.
Half-duplex: one call at a time; own streams are never re-bridged.

### Deployment

    go build -o /usr/local/bin/dvxbridge ./cmd/dvxbridge
    install -m 0640 deploy/dvxbridge.ini /etc/dvxbridge.ini      # edit
    install -m 0644 deploy/dvxbridge.service /etc/systemd/system/
    deploy/dmrids-update.sh                                      # radioid.net CSV; run daily
    systemctl enable --now dvxbridge

## Verification

| Test | Result |
|---|---|
| Golay vs mbelib; all weight-4 errors detected | Pass |
| FEC/deinterleave vs mbelib, 10,000 random frames | Bit-exact |
| Dequantiser vs mbelib, 6,000 stateful frames | ≤6e-6 |
| D-STAR C1 = Golay(24,12) + 24-bit PN | Matches MMDVMHost PRNG_TABLE (4096/4096) |
| Re-encode of real D-STAR with same codec | 100% pitch/V/UV index match, 0.00 dB |
| Real D-STAR → DMR output frames | 0 FEC errors, pitch within 50 ¢ on 100% voiced frames |
| Concealment of uncorrectable C0 | Output follows neighbour interpolation |
| DMR full LC, terminator, embedded LC, EMB, slot type | Bit-exact vs MMDVMHost (300 LCs, 128 EMB, 256 slot types) |
| Talker alias | Decodes with MMDVMHost's algorithm |
| D-STAR CRC | CRC-16/X-25 check value 0x906E |
| HBP login (SHA-256), RPTC length, DMRD | Fake-master loopback; wrong password refused |
| DExtra link/ACK, DSVT header/voice/last | Fake-reflector loopback |
| Bridge D-STAR → DMR | Real voice: header LC, sync/EMB, alias, GPS, terminator all decode; 0 FEC errors |
| Bridge DMR → D-STAR | Header callsign, 20-char text, DPRS position, seq 0–20, end frame |

## Benchmark (real D-STAR capture, 443 frames)

| D-STAR → DMR | Parametric | Tandem* |
|---|---|---|
| Pitch error > 50 ¢ | 0.00% | 5.26% |
| V/UV agreement | 98.0% | 55.8% |
| Log-spectral distance | 1.67 dB | 3.18 dB |
| Pitch jitter (source 57.8 ¢) | 56.8 ¢ | 85.0 ¢ |

| Hops D⇄M | Parametric LSD / pitch err | Tandem* LSD / pitch err |
|---|---|---|
| 1 | 1.67 dB / 0% | 3.18 dB / 5.3% |
| 4 | 2.28 dB / 0% | 4.49 dB / 33.2% |

| BER | Pass-through pitch err / jitter | dvxcode pitch err / jitter (ref ≈59 ¢) |
|---|---|---|
| 3% | 0.00% / 59.0 ¢ | 0.00% / 58.1 ¢ |
| 5% | 2.11% / 87.8 ¢ | 0.00% / 57.5 ¢ |
| 8% | 10.75% / 155.5 ¢ | 5.35% / 94.4 ¢ |

\* Tandem uses this project's analyser, not a DVSI chip; it overstates the gap.
Parametric figures do not depend on it.

## Anti-warble measures

| Cause | Measure |
|---|---|
| Tandem re-estimation | Parametric path only |
| Encoder/receiver predictor drift | Shadow decoder; closed-loop quantisation |
| Corrupted pitch word (R2-D2) | Extended-Golay detection → conceal; never pass garbage |
| Stalled pitch during concealment | 20 ms lookahead: interpolate between neighbours |
| D-STAR C1 failure, C0 good | Partial concealment: keep pitch/voicing/gain, borrow shape |
| Isolated octave outliers | Lookahead repair (neighbours agree, outlier > 1.6×) |
| Index flicker | Pitch and V/UV hysteresis |

## Reliability

| Measure | Detail |
|---|---|
| Fuzzing | `go test -fuzz` targets: AMBE FEC, DMR bursts/LC/TA/GPS, DSVT/header/slow data/DPRS, DMRD, transcoder (output always FEC-clean), bridge event loop |
| Input sanitising | Non-finite or out-of-range MBE targets → silence frame (counted) |
| Panic containment | Event handlers recover; active calls reset, network sessions keep running |
| Time-out timer | Calls force-ended after `MaxCall` (default 180 s) |
| Bounded latency | Output queues capped (default 3 s); DMR drops whole superframes to keep A–F/LC alignment |
| Reconnect | HomeBrew and DExtra: exponential backoff 2 s → 60 s with jitter; reset after a healthy session |
| Monitoring | `status = 127.0.0.1:9180` → `/status` JSON counters, `/healthz` 200/503 |

## Known limits

| Item | Detail |
|---|---|
| D-STAR pitch table | mbelib fitted formula; needs DVSI ground truth |
| D-STAR V/UV codebook | mbelib placeholder (16 entries) |
| Tones/DTMF | Muted, not translated |
| D-STAR protocols | DExtra only (no DPlus/DCS); late entry without header uses slow-data header |
| DMR | Group calls on one TG/slot; private calls and DMR data/SMS not bridged |
| On-air validation | Pending: loopback and real D-STAR capture only |
| DMR real-voice validation | No DMR voice capture yet (supplied DMR.mp3 is control/data channel only) |
| D-STAR b0 120–125 | Voice in real streams (observed); 126/127 tones; silence = exact null frame |

## Reproducing the test corpus

    ffmpeg -i DStar_Sound.mp3 -ac 1 -ar 48000 -sample_fmt s16 ds.wav
    go run ./tools/dstardemod -in ds.wav > testdata/vectors/dstar_real.hex

Patent notice: AMBE/AMBE+2 are DVSI technologies; see NOTICE.
