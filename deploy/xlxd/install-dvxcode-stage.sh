#!/bin/bash
set -Eeuo pipefail

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
readonly DVXCODE_ROOT="$REPO_ROOT/dvxcode"

[[ $EUID -eq 0 ]] || { echo "install-dvxcode-stage.sh must run as root" >&2; exit 77; }
command -v go >/dev/null
[[ -f "$DVXCODE_ROOT/go.mod" && -d "$DVXCODE_ROOT/cmd/dvxcode" && -d "$DVXCODE_ROOT/cmd/dvxbridge" ]] || {
    echo "DVxCode source is absent from $DVXCODE_ROOT" >&2
    exit 66
}

work_dir="$(mktemp -d /tmp/dvhub-dvxcode.XXXXXX)"
cleanup() { rm -rf -- "$work_dir"; }
trap cleanup EXIT

(
    cd "$DVXCODE_ROOT"
    go test -race ./...
    go vet ./...
    CGO_ENABLED=0 go build -trimpath -o "$work_dir/dvxcode" ./cmd/dvxcode
    CGO_ENABLED=0 go build -trimpath -o "$work_dir/dvxbridge" ./cmd/dvxbridge
)

install -d -m 0750 /etc/dvhub
install -m 0755 "$work_dir/dvxcode" /usr/local/bin/dvxcode.new
install -m 0755 "$work_dir/dvxbridge" /usr/local/bin/dvxbridge.new
install -o root -g dvhub -m 0640 "$DVXCODE_ROOT/deploy/dvxbridge.ini" /etc/dvhub/dvxbridge.ini.example
install -m 0644 "$SCRIPT_DIR/dvxbridge.service" /etc/systemd/system/dvxbridge.service

mv -f /usr/local/bin/dvxcode.new /usr/local/bin/dvxcode
mv -f /usr/local/bin/dvxbridge.new /usr/local/bin/dvxbridge
systemctl daemon-reload

# A staged installation must not inherit an obsolete production approval.
rm -f -- /etc/dvhub/dvxcode-validation.sha256
systemctl disable dvxbridge.service >/dev/null 2>&1 || true

echo "DVxCode codec and bridge binaries installed in staged, disabled state."
echo "No live configuration or validation marker was created; dashboard start remains fail-closed."
