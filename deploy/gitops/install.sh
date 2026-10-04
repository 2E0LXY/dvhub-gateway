#!/bin/bash
set -Eeuo pipefail

if [[ $EUID -ne 0 ]]; then
    echo "install.sh must run as root" >&2
    exit 77
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
for command in git go caddy curl python3 flock systemd-analyze; do
    command -v "$command" >/dev/null || { echo "missing required command: $command" >&2; exit 69; }
done

install -d -o root -g dvhub -m 0750 /etc/dvhub
install -d -m 0750 /var/lib/dvhub-deploy
install -m 0755 "$script_dir/dvhub-deploy" /usr/local/sbin/dvhub-deploy
install -m 0644 "$script_dir/dvhub-deploy.service" /etc/systemd/system/dvhub-deploy.service
install -m 0644 "$script_dir/dvhub-deploy.timer" /etc/systemd/system/dvhub-deploy.timer

if [[ ! -e /etc/dvhub/deploy.env ]]; then
    install -m 0640 "$script_dir/deploy.env.example" /etc/dvhub/deploy.env
fi

systemctl daemon-reload
systemctl enable --now dvhub-deploy.timer
echo "Git deployment timer installed. Run 'systemctl start dvhub-deploy.service' for the first deployment."
