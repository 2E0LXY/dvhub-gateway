# Yorkshire Link AllStar node 530471

The VPS runs official ASL3/Asterisk `app_rpt` as public hub node `530471`.
Its radio channel is the local USRP interface on ports `34001/32001`, connected
to `USRP2DMR-DV30` and the permanent DMR TG23530 conference.

The node password is never stored in this repository. An administrator enters
it through the authenticated DVHub dashboard. The gateway writes a mode-0600
staging file, invokes the narrowly scoped root helper, and the helper atomically
creates Asterisk's mode-0640 HTTP registration file before deleting the staging
copy. ASL3 HTTP registration is used; legacy IAX registration is not enabled.

`dvhub-allstar-control` validates numeric targets before issuing app_rpt link
or unlink commands. `dvhub-allstar-status` exposes only registration, link and
node status output; it never reads or prints the registration configuration.

## EchoLink

The authenticated dashboard can stage a validated EchoLink `-L` or `-R`
identity for `chan_echolink`. The password is written only to a mode-0600 JSON
staging file, read with `O_NOFOLLOW` and owner/mode checks by the narrowly
scoped root helper, then removed. The helper updates ASL3's existing `[el0]`
stanza, enables `chan_echolink.so`, restarts Asterisk and rolls both files back
if the module does not load. EchoLink UDP ports 5198-5199 must be allowed by
the VPS firewall, and the callsign/node must already be validated by EchoLink.
