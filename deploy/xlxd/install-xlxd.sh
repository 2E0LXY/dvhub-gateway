#!/bin/bash
set -Eeuo pipefail

readonly XLXD_REPOSITORY=https://github.com/LX3JL/xlxd.git
readonly XLXD_REVISION=bf5d0148dbdf2534af129ca3cc034c5051dcfc8d
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"

usage() {
    echo "usage: $0 --callsign XLXnnn --listen-ip ADDRESS [--with-dvxcode]" >&2
}

callsign=""
listen_ip=""
with_dvxcode=false
while (($#)); do
    case "$1" in
        --callsign) callsign="${2:-}"; shift 2 ;;
        --listen-ip) listen_ip="${2:-}"; shift 2 ;;
        --with-dvxcode) with_dvxcode=true; shift ;;
        *) usage; exit 64 ;;
    esac
done

[[ $EUID -eq 0 ]] || { echo "install-xlxd.sh must run as root" >&2; exit 77; }
[[ $callsign =~ ^XLX[0-9]{3}$ ]] || { echo "--callsign must be XLX followed by three digits" >&2; exit 64; }
[[ -n $listen_ip ]] || { usage; exit 64; }
command -v git >/dev/null
command -v g++ >/dev/null
command -v make >/dev/null

work_dir="$(mktemp -d /tmp/dvhub-xlxd.XXXXXX)"
cleanup() { rm -rf -- "$work_dir"; }
trap cleanup EXIT

git init -q "$work_dir/xlxd"
git -C "$work_dir/xlxd" remote add origin "$XLXD_REPOSITORY"
git -C "$work_dir/xlxd" fetch -q --depth 1 origin "$XLXD_REVISION"
git -C "$work_dir/xlxd" checkout -q --detach FETCH_HEAD
[[ $(git -C "$work_dir/xlxd" rev-parse HEAD) == "$XLXD_REVISION" ]]
git -C "$work_dir/xlxd" apply --unidiff-zero --check "$SCRIPT_DIR/xlxd-dstar-only.patch"
git -C "$work_dir/xlxd" apply --unidiff-zero "$SCRIPT_DIR/xlxd-dstar-only.patch"
make -C "$work_dir/xlxd/src" clean all -j"$(nproc)"

install -m 0755 "$work_dir/xlxd/src/xlxd" /usr/local/bin/xlxd.new
install -d -m 0750 /etc/dvhub
install -d -m 0755 /etc/xlxd
install -m 0644 "$SCRIPT_DIR/xlxd.whitelist" /etc/xlxd/xlxd.whitelist
install -m 0644 "$SCRIPT_DIR/xlxd.blacklist" /etc/xlxd/xlxd.blacklist
install -m 0644 "$SCRIPT_DIR/xlxd.interlink" /etc/xlxd/xlxd.interlink
install -m 0644 "$SCRIPT_DIR/xlxd.terminal" /etc/xlxd/xlxd.terminal
printf 'XLX_DISPLAY_NAME=XLXYOR\nXLX_CALLSIGN=%s\nXLX_LISTEN_IP=%s\nXLX_TRANSCODER_IP=127.0.0.1\n' \
    "$callsign" "$listen_ip" > /etc/dvhub/xlxd.env.new
chmod 0640 /etc/dvhub/xlxd.env.new
chown root:dvhub /etc/dvhub/xlxd.env.new
install -m 0644 "$SCRIPT_DIR/xlxd.service" /etc/systemd/system/xlxd.service

if $with_dvxcode; then
    [[ -f "$REPO_ROOT/dvxcode/go.mod" && -d "$REPO_ROOT/dvxcode/cmd/dvxbridge" ]] || {
        echo "DVxCode source is absent: expected dvxcode/go.mod and dvxcode/cmd/dvxbridge" >&2
        exit 66
    }
    [[ -s "$REPO_ROOT/dvxcode/VALIDATION.md" ]] || {
        echo "DVxCode has no recorded DV30 validation; refusing to install the bridge" >&2
        exit 65
    }
    (cd "$REPO_ROOT/dvxcode" && go test -race ./... && go vet ./... && \
        CGO_ENABLED=0 go build -trimpath -o "$work_dir/dvxbridge" ./cmd/dvxbridge)
    [[ -f "$REPO_ROOT/dvxcode/deploy/dvxbridge.ini" ]] || {
        echo "DVxCode deployment config is absent" >&2
        exit 66
    }
    install -m 0755 "$work_dir/dvxbridge" /usr/local/bin/dvxbridge.new
    install -o root -g dvhub -m 0640 "$REPO_ROOT/dvxcode/deploy/dvxbridge.ini" /etc/dvhub/dvxbridge.ini.example
    sha256sum "$REPO_ROOT/dvxcode/VALIDATION.md" | awk '{print $1}' > /etc/dvhub/dvxcode-validation.sha256.new
    chown root:dvhub /etc/dvhub/dvxcode-validation.sha256.new
    chmod 0640 /etc/dvhub/dvxcode-validation.sha256.new
    install -m 0644 "$SCRIPT_DIR/dvxbridge.service" /etc/systemd/system/dvxbridge.service
fi

mv -f /usr/local/bin/xlxd.new /usr/local/bin/xlxd
mv -f /etc/dvhub/xlxd.env.new /etc/dvhub/xlxd.env
if [[ -e /usr/local/bin/dvxbridge.new ]]; then
    mv -f /usr/local/bin/dvxbridge.new /usr/local/bin/dvxbridge
    mv -f /etc/dvhub/dvxcode-validation.sha256.new /etc/dvhub/dvxcode-validation.sha256
fi
systemctl daemon-reload
systemd-analyze verify /etc/systemd/system/xlxd.service
if $with_dvxcode; then
    systemd-analyze verify /etc/systemd/system/dvxbridge.service
fi

echo "Installed the pinned D-STAR-only XLXd build for $callsign."
echo "Services remain disabled. Review the XLX number, firewall and DVxCode shadow configuration before enabling them."
