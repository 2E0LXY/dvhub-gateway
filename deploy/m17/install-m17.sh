#!/bin/bash
set -Eeuo pipefail

readonly MREFD_REPO="https://github.com/n7tae/mrefd.git"
readonly MREFD_SHA="7ba8c9dc3de2a43f44ba05e53b010c26c4147f2f"
readonly MMDVM_REPO="https://github.com/ShaYmez/MMDVM_CM.git"
readonly MMDVM_SHA="5c0a387da2cedfa47fc171fc57c48fee252c91aa"

if [[ $EUID -ne 0 ]]; then
    echo "install-m17.sh must run as root" >&2
    exit 77
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
work_dir="$(mktemp -d /tmp/dvhub-m17.XXXXXX)"
backup_dir="/var/lib/dvhub-m17/backups/$(date -u +%Y%m%dT%H%M%SZ)"
installed=false

cleanup() { rm -rf -- "$work_dir"; }
rollback() {
    status=$?
    trap - ERR
    echo "M17 installation failed; restoring Asterisk configuration" >&2
    if [[ -e "$backup_dir/rpt.conf" ]]; then
        install -o root -g asterisk -m 0644 "$backup_dir/rpt.conf" /etc/asterisk/rpt.conf
        systemctl restart asterisk.service || true
    fi
    if ! $installed; then
        systemctl disable --now usrp2m17.service mrefd.service >/dev/null 2>&1 || true
    fi
    exit "$status"
}
trap cleanup EXIT
trap rollback ERR

for command in apt-get git make g++ python3 systemctl install; do
    command -v "$command" >/dev/null || { echo "missing required command: $command" >&2; exit 69; }
done

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends build-essential git nlohmann-json3-dev

if systemctl is-enabled --quiet mrefd.service 2>/dev/null; then
    installed=true
fi

install -d -m 0750 "$backup_dir"
cp -a -- /etc/asterisk/rpt.conf "$backup_dir/rpt.conf"

git clone --quiet "$MREFD_REPO" "$work_dir/mrefd"
git -C "$work_dir/mrefd" checkout --quiet "$MREFD_SHA"
install -m 0644 "$script_dir/mrefd.mk" "$work_dir/mrefd/mrefd.mk"
make -C "$work_dir/mrefd" -j"$(nproc)"

git clone --quiet "$MMDVM_REPO" "$work_dir/mmdvm-cm"
git -C "$work_dir/mmdvm-cm" checkout --quiet "$MMDVM_SHA"
git -C "$work_dir/mmdvm-cm" apply --check "$script_dir/usrp2m17-debian13.patch"
git -C "$work_dir/mmdvm-cm" apply "$script_dir/usrp2m17-debian13.patch"
make -C "$work_dir/mmdvm-cm/USRP2M17" -j"$(nproc)"

id m17 >/dev/null 2>&1 || useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin m17
install -d -o root -g m17 -m 0750 /etc/mrefd /etc/dvhub
install -d -o m17 -g m17 -m 0750 /var/log/mrefd /var/log/usrp2m17
install -m 0755 "$work_dir/mrefd/mrefd" /usr/local/bin/mrefd
install -m 0755 "$work_dir/mmdvm-cm/USRP2M17/USRP2M17" /usr/local/bin/USRP2M17
install -o root -g m17 -m 0640 "$script_dir/mrefd.cfg" /etc/mrefd/mrefd.cfg
install -o root -g m17 -m 0640 "$script_dir/mrefd.blacklist" /etc/mrefd/mrefd.blacklist
install -o root -g m17 -m 0640 "$script_dir/mrefd.whitelist" /etc/mrefd/mrefd.whitelist
install -o root -g m17 -m 0640 "$script_dir/mrefd.interlink" /etc/mrefd/mrefd.interlink
install -o root -g m17 -m 0640 "$script_dir/USRP2M17.ini" /etc/dvhub/USRP2M17.ini
install -m 0644 "$script_dir/mrefd.service" /etc/systemd/system/mrefd.service
install -m 0644 "$script_dir/usrp2m17.service" /etc/systemd/system/usrp2m17.service

python3 "$script_dir/configure-asl-m17.py"
chown root:asterisk /etc/asterisk/rpt.conf
chmod 0644 /etc/asterisk/rpt.conf

systemd-analyze verify /etc/systemd/system/mrefd.service /etc/systemd/system/usrp2m17.service
systemctl daemon-reload
systemctl enable mrefd.service usrp2m17.service
systemctl restart asterisk.service
systemctl restart mrefd.service
systemctl restart usrp2m17.service

for _ in {1..20}; do
    local_nodes="$(asterisk -rx 'rpt localnodes' 2>/dev/null || true)"
    links="$(asterisk -rx 'rpt nodes 1998' 2>/dev/null || true)"
    if systemctl is-active --quiet asterisk.service mrefd.service usrp2m17.service \
        && grep -q '1998' <<<"$local_nodes" \
        && grep -q '530471' <<<"$links" \
        && ss -lun | grep -qE '[:.]17000[[:space:]]'; then
        trap - ERR
        echo "M17-YLH A is active through private AllStar node 1998 to node 530471"
        echo "$local_nodes"
        echo "$links"
        exit 0
    fi
    sleep 1
done

echo "M17 services did not pass their post-install checks" >&2
exit 1
