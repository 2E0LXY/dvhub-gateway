# Passive quality indicators

These are application alerting policies, not guarantees of intelligibility or standards for end-to-end latency. No probes or test calls are added by the checks.

| Indicator | Warning | Fault |
|---|---|---|
| Host interface loss | >=0.5% and >=3 drops, or >=3 interface errors | >=2% and >=5 drops, or >=5 errors |
| DMR sequence loss | >=0.5% and >=3 missing frames | >=2% and >=5 missing frames |
| DMR playout-gap proxy | >=3 gap events and >=1% event rate | >=5 gap events and >=5% event rate |
| Successful AMBE conversion p95 | >=150ms with >=20 successful samples | >=300ms with >=20 successful samples |
| AMBE failures | >=2 failures and >=0.5%, or any failures with >=10% failure rate | >=3 failures and >=2% failure rate |

Host sampling is five seconds; data older than 20 seconds is neutral. DMR and vocoder alerts use the last 30 seconds rather than holding old faults for five minutes. No recent media is neutral, not green and not a fault. Counts and rates remain visible even when there is insufficient evidence to alert.

DMR arrival jitter is not end-to-end latency or measured audible stutter. Sequence duplicates/reordered packets do not become new timing-gap samples. A gap is missing sequence data or an interval greater than 120ms; the event-rate policy avoids alarming over isolated events.

AMBE latency statistics include successful conversions only. Timeouts count separately as failures. The real 100ms hardware exchange deadline is NOT raised by these display thresholds. A displayed 100ms timeout with repeated failures remains a fault; a successful response near 100ms alone does not. Higher latency thresholds provide future headroom, not permission for the current client to wait longer or to miss audio deadlines.

Service failures, offline routes and rejected network logins retain their fault states. No authentication, watchdog, TX limit or packet handling deadline is weakened.
