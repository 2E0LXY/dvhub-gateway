#!/usr/bin/env bash
set -Eeuo pipefail

readonly MMDVM_REPO="https://github.com/ShaYmez/MMDVM_CM.git"
readonly MMDVM_SHA="5c0a387da2cedfa47fc171fc57c48fee252c91aa"

if [[ $EUID -ne 0 ]]; then
    echo "install-identity-bridge.sh must run as root" >&2
    exit 1
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/../.." && pwd)"
work_dir="$(mktemp -d /tmp/dvhub-identity-build.XXXXXX)"
backup_dir="$(mktemp -d /var/lib/dvhub-deploy/identity-backup.XXXXXX)"
installed=0

cleanup() {
    rm -rf -- "$work_dir"
}

rollback() {
    local status=$?
    if (( installed )); then
        for binary in USRP2DMR-DV30 USRP2M17; do
            if [[ -f "$backup_dir/$binary" ]]; then
                install -m 0755 "$backup_dir/$binary" "/usr/local/bin/$binary"
            fi
        done
        systemctl daemon-reload
        systemctl restart usrp2dmr.service usrp2m17.service || true
    fi
    cleanup
    exit "$status"
}
trap rollback ERR
trap cleanup EXIT

git clone --quiet "$MMDVM_REPO" "$work_dir/mmdvm-cm"
git -C "$work_dir/mmdvm-cm" checkout --quiet "$MMDVM_SHA"

git -C "$work_dir/mmdvm-cm" apply --check "$script_dir/usrp2dmr-dv30.patch"
git -C "$work_dir/mmdvm-cm" apply "$script_dir/usrp2dmr-dv30.patch"
git -C "$work_dir/mmdvm-cm" apply --check "$script_dir/usrp2dmr-identity.patch"
git -C "$work_dir/mmdvm-cm" apply "$script_dir/usrp2dmr-identity.patch"
make -C "$work_dir/mmdvm-cm/USRP2DMR" -j"$(nproc)"

git -C "$work_dir/mmdvm-cm" apply --check "$repo_root/deploy/m17/usrp2m17-debian13.patch"
git -C "$work_dir/mmdvm-cm" apply "$repo_root/deploy/m17/usrp2m17-debian13.patch"
git -C "$work_dir/mmdvm-cm" apply --check "$repo_root/deploy/m17/usrp2m17-identity.patch"
git -C "$work_dir/mmdvm-cm" apply "$repo_root/deploy/m17/usrp2m17-identity.patch"
make -C "$work_dir/mmdvm-cm/USRP2M17" -j"$(nproc)"

install -m 0755 /usr/local/bin/USRP2DMR-DV30 "$backup_dir/USRP2DMR-DV30"
install -m 0755 /usr/local/bin/USRP2M17 "$backup_dir/USRP2M17"
installed=1

systemctl stop usrp2dmr.service usrp2m17.service
install -m 0755 "$work_dir/mmdvm-cm/USRP2DMR/USRP2DMR" /usr/local/bin/USRP2DMR-DV30
install -m 0755 "$work_dir/mmdvm-cm/USRP2M17/USRP2M17" /usr/local/bin/USRP2M17
install -m 0644 "$script_dir/usrp2dmr.service" /etc/systemd/system/usrp2dmr.service
install -m 0644 "$repo_root/deploy/m17/usrp2m17.service" /etc/systemd/system/usrp2m17.service
systemctl daemon-reload
systemctl start usrp2dmr.service usrp2m17.service

systemctl is-active --quiet usrp2dmr.service
systemctl is-active --quiet usrp2m17.service
ss -lun | grep -qE '127\.0\.0\.1:35171[[:space:]]'
ss -lun | grep -qE '127\.0\.0\.1:35172[[:space:]]'

trap - ERR
echo "Cross-mode identity side-channel installed and both converters are active."
