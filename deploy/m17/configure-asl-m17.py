#!/usr/bin/python3
"""Add the private M17 USRP node to an ASL3 rpt.conf idempotently."""

from pathlib import Path
import re
import sys


RPT_PATH = Path("/etc/asterisk/rpt.conf")
NODE_LINE = "1998 = radio@127.0.0.1/1998,NONE"
NODE_BLOCK = """[1998](node-main)
rxchannel = USRP/127.0.0.1:34017:32017
duplex = 0
linktolink = yes
telemdefault = 0
nounkeyct = 1
idrecording = |i2E0LXY
idtime = 0
startup_macro = pppppppppp *813530471
"""


def configure(text: str) -> str:
    active_node = re.compile(
        r"(?m)^1998[ \t]*=[ \t]*radio@127\.0\.0\.1/1998,NONE[ \t]*$"
    )
    if not active_node.search(text):
        public = "530471 = radio@127.0.0.1/530471,NONE"
        if public not in text:
            raise RuntimeError("public node 530471 is missing from [nodes]")
        text = text.replace(public, public + "\n" + NODE_LINE, 1)

    permanent = re.compile(r"(?m)^;[ \t]*813[ \t]*=[ \t]*ilink,13.*$")
    if permanent.search(text):
        text = permanent.sub(
            "813 = ilink,13                    ; Permanently connect specified link -- transceive",
            text,
            count=1,
        )
    elif not re.search(r"(?m)^813[ \t]*=[ \t]*ilink,13(?:[ \t;]|$)", text):
        raise RuntimeError("app_rpt permanent-link function 813 is unavailable")

    node = re.compile(r"(?ms)^\[1998\]\(node-main\)\n.*?(?=\n\[|\Z)")
    if node.search(text):
        text = node.sub(NODE_BLOCK.rstrip() + "\n", text, count=1)
    else:
        text = text.rstrip() + "\n\n" + NODE_BLOCK
    return text


def main() -> int:
    original = RPT_PATH.read_text(encoding="utf-8")
    updated = configure(original)
    if updated == original:
        return 0
    temporary = RPT_PATH.with_name(RPT_PATH.name + ".dvhub-m17-new")
    temporary.write_text(updated, encoding="utf-8")
    temporary.chmod(RPT_PATH.stat().st_mode)
    temporary.replace(RPT_PATH)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"M17 AllStar configuration failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
