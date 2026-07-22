#!/bin/sh
# SPDX-License-Identifier: GPL-2.0-or-later
set -eu

if [ "$(id -u)" -ne 0 ]; then
    echo "Run this installer as root" >&2
    exit 1
fi

install -d -m 0755 /opt/dvhub-ambe-server /etc/dvhub
install -m 0755 dvhub_ambe_server.py /opt/dvhub-ambe-server/dvhub_ambe_server.py
install -m 0644 dvhub-ambe-server.service /etc/systemd/system/dvhub-ambe-server.service
if ! getent group dvhub-ambe >/dev/null 2>&1; then
    groupadd --system dvhub-ambe
fi
if ! getent passwd dvhub-ambe >/dev/null 2>&1; then
    useradd --system --gid dvhub-ambe --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin dvhub-ambe
fi
if [ ! -f /etc/dvhub/ambe-server.env ]; then
    install -m 0640 -o root -g dvhub-ambe ambe-server.env.example /etc/dvhub/ambe-server.env
fi
systemctl daemon-reload
echo "Edit /etc/dvhub/ambe-server.env, then run: systemctl enable --now dvhub-ambe-server"
