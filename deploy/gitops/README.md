# CI-gated Git deployment

This deployer checks `main` every 15 minutes and installs a revision only after
the GitHub `verify` and `build` checks have both completed successfully. It
builds in an isolated staging directory, validates the Go project, Caddyfile
and systemd unit, installs an allowlisted set of files atomically, checks the
gateway socket, and rolls back on failure.

Go module, build and downloaded toolchain caches are kept under
`/var/lib/dvhub-deploy/go`; the hardened service does not require access to
`/root` or any user's home directory.

The Caddyfile uses the backwards-compatible `basicauth` directive spelling so
it validates on both Debian's pre-2.8 Caddy package and current Caddy releases.

It updates only:

- `/usr/local/bin/dvhub-gateway`
- `/var/www/dvhub/dashboard.html`
- `/var/www/dvhub/ysf-status.html`
- `/etc/systemd/system/dvhub-gateway.service`
- its own deploy script, service and timer (after shell syntax validation)

The repository Caddyfile is validated on every deployment but is not installed
or reloaded automatically. The live proxy has host-specific authentication and
certificate environment, so unattended replacement is disabled by default.
Set `DVHUB_DEPLOY_CADDY=1` in `/etc/dvhub/deploy.env` only for an attended
Caddy rollout after its environment has been verified.

It deliberately does **not** copy `/etc/dvhub`, `/etc/asterisk`, reflector or
cross-mode configuration, passwords, API keys, radio identities, or vocoder
allowlists. The running YSF, P25, NXDN, AllStar and conference services are not
restarted.

## First installation on the VPS

From a trusted checkout of the repository:

```sh
sudo deploy/gitops/install.sh
sudo systemctl start dvhub-deploy.service
sudo journalctl -u dvhub-deploy.service -n 100 --no-pager
```

The timer status is available with:

```sh
systemctl list-timers dvhub-deploy.timer
cat /var/lib/dvhub-deploy/current-sha
```

`dvhub-deploy --force` exists for attended recovery only. It bypasses the CI
gate and should not be used by the timer.

## Security boundary

Automatic deployment makes write access to the GitHub `main` branch part of
the VPS trust boundary. Enable branch protection, require both checks, require
two-factor authentication for collaborators, and do not commit secrets. The
deployer retains five rollback snapshots under `/var/lib/dvhub-deploy/backups`.
