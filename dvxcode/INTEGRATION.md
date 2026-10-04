# dvxcode in dvhub-gateway (staged, not wired in)

Self-contained Go module (`github.com/2E0LXY/dvxcode`). **Not imported** by the
gateway: root `go build/test/vet ./...` ignore it, and the gateway's fail-closed
hardware-AMBE policy is unchanged.

| Item | State |
|---|---|
| Root module, `gateway.go`, dashboard, `deploy/`, existing workflows | Untouched |
| CI | `.github/workflows/dvxcode.yml`, path-filtered to `dvxcode/**` |
| Golden vectors | Generated in CI from pinned mbelib/dsd/MMDVMHost commits |

## Integration options (when ready)

| Option | Effort | Notes |
|---|---|---|
| Run `dvxbridge` as its own systemd service | Low | `deploy/dvxbridge.ini`, `deploy/dvxbridge.service`; HomeBrew + DExtra |
| Import `xcode`/`ambe`/`mbe` into the gateway | Medium | `replace` directive or `go.work`; parametric D-STAR⇄AMBE+2 beside the DV30 pool |
| DV30 ground truth for D-STAR | Medium | Pitch-sweep calibration of the D-STAR pitch/V/UV tables (README "Known limits") |

Policy note: the gateway README states "no synthetic AMBE fallback". dvxcode
never synthesises PCM; it re-quantises decoded MBE parameters closed-loop.
Decide whether that meets the policy before wiring it in.
