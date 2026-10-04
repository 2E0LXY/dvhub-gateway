#!/bin/sh
# Refresh the DMR ID database and tell dvxbridge to reload (run daily from cron/systemd timer).
set -eu
dst=/var/lib/dvxbridge/DMRIds.dat
tmp=$(mktemp)
curl -fsSL https://radioid.net/static/user.csv -o "$tmp"
[ "$(wc -l < "$tmp")" -gt 100000 ] || { echo "download too small, keeping old file" >&2; rm -f "$tmp"; exit 1; }
install -m 0644 "$tmp" "$dst" && rm -f "$tmp"
systemctl reload dvxbridge
