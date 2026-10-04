# DVxCode integration gates

DVxCode is an optional, isolated AMBE-to-AMBE+2 bridge for the Yorkshire Link
Hub. It does not replace the DV30 hardware used for PCM-to-AMBE conversion and
it must not be imported into the main gateway until its interoperability has
been proved.

## Required repository layout

The uploaded source must be a nested Go module:

```text
dvxcode/
  go.mod
  cmd/dvxcode/
  cmd/dvxbridge/
  cmd/mbedump/
  deploy/dvxbridge.ini
  VALIDATION.md
```

Generated reference binaries, golden text vectors, `*.dat` identity files and
`go.work` are not committed. The nested module keeps the root gateway build
independent until validation is complete.

## Release sequence

| Stage | Work | Exit condition |
| --- | --- | --- |
| A | Add the isolated DVxCode module and its path-filtered CI | DVxCode tests, race detector and vet pass |
| B | Compare both conversion directions against the allowlisted DV30 | Speech, silence, error concealment and real-radio tests recorded as passing |
| C | Link XLXd module A to a dedicated, unbridged DMR test TG | At least one week of clean shadow logs and listener sign-off |
| D | Enable the production cross-mode service | Operator approval and explicit vocoder-policy update |
| E | Consider in-process integration | Only if the separate service is insufficient |

## DV30 acceptance gate

`VALIDATION.md` must record D-STAR pitch and V/UV calibration, D-STAR-to-DMR,
DMR-to-D-STAR, silence/null frames, 3% and 5% BER behaviour, and real-radio
tests. Each converted stream is decoded by the DV30 for the comparison. Any
failed interoperability row stops the release at stage B.

## Runtime boundaries

- `dvxbridge` runs as its own hardened systemd service.
- The bridge joins XLXd over DExtra and the test DMR master over HBP.
- The test TG is absent from all gateway conference routes.
- Self-originated stream IDs are discarded to prevent loops.
- DMR IDs and D-STAR callsigns are resolved through the maintained identity
  data; unknown identities use the configured bridge identity.
- The production service remains fail-closed and never falls back to the
  retired experimental software speech codec.

The deployable XLXd profile and installer are documented in
`deploy/xlxd/README.md`.
