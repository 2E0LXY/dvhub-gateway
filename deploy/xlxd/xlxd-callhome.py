#!/usr/bin/env python3
"""Publish an XLXd reflector to the upstream XLX directory.

The ownership token is generated locally once and is never logged or sent
anywhere except the configured XLX directory endpoint.
"""

from __future__ import annotations

import html
import os
import re
import secrets
import string
import sys
import time
import urllib.parse
import urllib.request
from pathlib import Path

ENV_FILE = Path("/etc/dvhub/xlxd.env")
XML_FILE = Path("/var/lib/xlxd/xlxd.xml")
KEY_FILE = Path("/var/lib/xlxd/callinghome.key")
CALLSIGN_RE = re.compile(r"^XLX[A-Z0-9]{3}$")
VERSION_RE = re.compile(r"<Version>([^<]+)</Version>")


def load_environment(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        values[key.strip()] = value
    return values


def load_or_create_key(path: Path) -> str:
    if path.exists():
        key = path.read_text(encoding="ascii").strip()
        if not re.fullmatch(r"[A-Za-z0-9]{16}", key):
            raise RuntimeError("invalid persisted XLX call-home key")
        return key
    alphabet = string.ascii_letters + string.digits
    key = "".join(secrets.choice(alphabet) for _ in range(16))
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="ascii") as stream:
        stream.write(key + "\n")
    return key


def process_uptime(process_name: str) -> int:
    ticks = os.sysconf("SC_CLK_TCK")
    boot_uptime = float(Path("/proc/uptime").read_text(encoding="ascii").split()[0])
    for entry in Path("/proc").iterdir():
        if not entry.name.isdigit():
            continue
        try:
            if (entry / "comm").read_text(encoding="ascii").strip() != process_name:
                continue
            fields = (entry / "stat").read_text(encoding="ascii").split()
            return max(0, int(boot_uptime - (int(fields[21]) / ticks)))
        except (FileNotFoundError, PermissionError, IndexError, ValueError):
            continue
    raise RuntimeError("xlxd is not running")


def main() -> int:
    env = load_environment(ENV_FILE)
    callsign = env.get("XLX_CALLSIGN", "").upper()
    if not CALLSIGN_RE.fullmatch(callsign):
        raise RuntimeError("XLX_CALLSIGN must match XLX plus three letters or digits")
    dashboard_url = env.get("XLX_DASHBOARD_URL", "")
    endpoint = env.get("XLX_CALLHOME_URL", "")
    if urllib.parse.urlparse(dashboard_url).scheme != "https":
        raise RuntimeError("XLX_DASHBOARD_URL must use HTTPS")
    if endpoint != "http://xlxapi.rlx.lu/api.php":
        raise RuntimeError("unexpected XLX call-home endpoint")

    status_xml = XML_FILE.read_text(encoding="utf-8", errors="replace")
    match = VERSION_RE.search(status_xml)
    if not match:
        raise RuntimeError("XLXd status XML has no version")
    key = load_or_create_key(KEY_FILE)
    esc = lambda value: html.escape(str(value), quote=True)
    payload = (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n'
        "<query>CallingHome</query>\n"
        "<reflector>\n"
        f"<name>{esc(callsign)}</name>\n"
        f"<uptime>{process_uptime('xlxd')}</uptime>\n"
        f"<hash>{esc(key)}</hash>\n"
        f"<url>{esc(dashboard_url)}</url>\n"
        f"<country>{esc(env.get('XLX_CALLHOME_COUNTRY', 'United Kingdom'))}</country>\n"
        f"<comment>{esc(env.get('XLX_CALLHOME_COMMENT', 'Yorkshire Link Hub'))}</comment>\n"
        f"<ip>{esc(env.get('XLX_LISTEN_IP', ''))}</ip>\n"
        f"<reflectorversion>{esc(match.group(1).strip())}</reflectorversion>\n"
        "</reflector>\n<interlinks></interlinks>"
    )
    request = urllib.request.Request(
        endpoint,
        data=urllib.parse.urlencode({"xml": payload}).encode("ascii"),
        headers={"Content-Type": "application/x-www-form-urlencoded"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=15) as response:
        if response.status != 200:
            raise RuntimeError(f"XLX directory returned HTTP {response.status}")
        response.read(4096)
    print(f"Published {callsign} to the XLX directory at {time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"XLX call-home failed: {exc}", file=sys.stderr)
        sys.exit(1)
