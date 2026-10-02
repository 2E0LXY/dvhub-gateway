#!/bin/sh
set -eu

node=530471
rpt=/etc/asterisk/rpt.conf
modules=/etc/asterisk/modules.conf
registration=/etc/asterisk/rpt_http_registrations.conf

[ -e "$rpt.dvhub-before-530471" ] || cp -a "$rpt" "$rpt.dvhub-before-530471"
[ -e "$modules.dvhub-before-530471" ] || cp -a "$modules" "$modules.dvhub-before-530471"

sed -i 's|^1999 = radio@127\.0\.0\.1/1999,NONE$|530471 = radio@127.0.0.1/530471,NONE|' "$rpt"
sed -i 's|^\[1999\](node-main)$|[530471](node-main)|' "$rpt"
sed -i '/^\[530471\](node-main)$/,/^$/ { /^duplex = 0$/d; /^linktolink = yes$/d; /^telemdefault = 0$/d; /^nounkeyct = 1$/d; /^idrecording = |i2E0LXY$/d; /^statpost_url = http:\/\/stats\.allstarlink\.org\/uhandler$/d; /^statpost_time = 60$/d; }' "$rpt"
sed -i '/^\[530471\](node-main)$/,/^$/ s@^rxchannel = .*$@rxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0\nlinktolink = yes\ntelemdefault = 0\nnounkeyct = 1\nidrecording = |i2E0LXY\nstatpost_url = http://stats.allstarlink.org/uhandler\nstatpost_time = 60@' "$rpt"
sed -i 's|^noload  = chan_usrp\.so.*$|load    = chan_usrp.so                   ; USRP Channel Module|' "$modules"

cat > "$registration" <<'EOF'
[general]
register_interval = 180

[registrations]
; Node 530471 is provisioned through the authenticated DVHub dashboard.
EOF

chown root:asterisk "$rpt" "$modules" "$registration"
chmod 0640 "$registration"
chmod 0644 "$rpt" "$modules"

systemctl disable --now iax-bridge.service >/dev/null 2>&1 || true
if ! systemctl restart asterisk.service || ! systemctl is-active --quiet asterisk.service; then
    cp -a "$rpt.dvhub-before-530471" "$rpt"
    cp -a "$modules.dvhub-before-530471" "$modules"
    systemctl restart asterisk.service
    echo "ASL node configuration failed; the previous Asterisk configuration was restored" >&2
    exit 1
fi

local_nodes=$(asterisk -rx 'rpt localnodes' 2>&1 || true)
usrp_modules=$(asterisk -rx 'module show like chan_usrp' 2>&1 || true)
printf '%s\n' "$local_nodes"
printf '%s\n' "$usrp_modules"
if ! printf '%s\n' "$local_nodes" | grep -q '530471' || ! printf '%s\n' "$usrp_modules" | grep -q 'chan_usrp.so'; then
    cp -a "$rpt.dvhub-before-530471" "$rpt"
    cp -a "$modules.dvhub-before-530471" "$modules"
    systemctl restart asterisk.service
    echo "ASL node verification failed; the previous Asterisk configuration was restored" >&2
    exit 1
fi
