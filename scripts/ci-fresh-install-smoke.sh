#!/bin/sh
set -eu

ARTIFACT="${1:-build/minimalrouter-linux-amd64.tar.gz}"
[ -f "$ARTIFACT" ] || {
    echo "Missing distribution artifact: $ARTIFACT" >&2
    exit 1
}

BUILD_DIR="$(cd "$(dirname "$ARTIFACT")" && pwd)"
ARTIFACT_NAME="$(basename "$ARTIFACT")"

docker run --rm --privileged \
    -v "$BUILD_DIR:/artifacts:ro" \
    -e "ARTIFACT_NAME=$ARTIFACT_NAME" \
    alpine:3.22 sh -euxs <<'SMOKE'
apk add --no-cache curl jq iproute2

mkdir -p /tmp/minimalrouter-dist
tar xzf "/artifacts/$ARTIFACT_NAME" -C /tmp/minimalrouter-dist
DIST_DIR="$(find /tmp/minimalrouter-dist -mindepth 1 -maxdepth 1 -type d | head -1)"
[ -n "$DIST_DIR" ]
cd "$DIST_DIR"

sh -n install.sh
sh install.sh

test -x /usr/bin/routerd
test -x /usr/sbin/router-applyd
test -x /etc/init.d/routerd
test -x /etc/init.d/router-applyd
test -x /etc/init.d/pppoe-wan
test -f /usr/share/minimalrouter/web/index.html
test "$(stat -c %a /var/lib/minimalrouter)" = "700"
test "$(stat -c %a /var/lib/minimalrouter-applyd)" = "700"

if ! ip link show eth1 >/dev/null 2>&1; then
    ip link add eth1 type dummy
fi
ip link set eth1 up

/usr/sbin/router-applyd >/tmp/router-applyd.log 2>&1 &
APPLYD_PID=$!
/usr/bin/routerd >/tmp/routerd.log 2>&1 &
ROUTERD_PID=$!

cleanup() {
    status=$?
    kill "$ROUTERD_PID" "$APPLYD_PID" >/dev/null 2>&1 || true
    if [ "$status" -ne 0 ]; then
        echo "===== router-applyd.log =====" >&2
        cat /tmp/router-applyd.log >&2 || true
        echo "===== routerd.log =====" >&2
        cat /tmp/routerd.log >&2 || true
        echo "===== nftables =====" >&2
        nft list ruleset >&2 || true
        echo "===== addresses =====" >&2
        ip -4 address >&2 || true
    fi
    exit "$status"
}
trap cleanup EXIT INT TERM

ready=0
for _ in $(seq 1 90); do
    if curl -kfsS https://192.168.1.1:8443/api/v1/setup/status >/tmp/setup-status-before.json 2>/dev/null; then
        ready=1
        break
    fi
    sleep 1
done
[ "$ready" -eq 1 ]
jq -e '.is_configured == false and .lan_interface == "eth1" and .lan_ip == "192.168.1.1"' /tmp/setup-status-before.json

curl -kfsS \
    -c /tmp/minimalrouter.cookies \
    -H "Content-Type: application/json" \
    --data '{
        "wan_interface": "eth0",
        "lan_interface": "eth1",
        "pppoe_username": "",
        "pppoe_password": "",
        "admin_password": "clean-install-admin-password-123!",
        "lan_ip_address": "192.168.1.1"
    }' \
    https://192.168.1.1:8443/api/v1/setup/apply \
    >/tmp/setup-result.json
jq -e '.success == true and (.csrf_token | length) > 20' /tmp/setup-result.json

curl -kfsS -b /tmp/minimalrouter.cookies \
    https://192.168.1.1:8443/api/v1/setup/status \
    >/tmp/setup-status-after.json
jq -e '.is_configured == true' /tmp/setup-status-after.json

curl -kfsS -b /tmp/minimalrouter.cookies \
    https://192.168.1.1:8443/api/v1/config \
    >/tmp/config.json
jq -e '
    .wan.enabled == false and
    .lan.interface == "eth1" and
    .lan.ip_address == "192.168.1.1" and
    .dhcp.enabled == true and
    .dhcp.range_start == "192.168.1.100" and
    .dhcp.range_end == "192.168.1.200" and
    .firewall.default_wan_input_policy == "deny" and
    .firewall.stateful_firewall == true and
    .wireguard.enabled == false and
    .cloudflare.ddns_enabled == false and
    .cloudflare.tunnel_enabled == false and
    .wifi.enabled == false
' /tmp/config.json

curl -kfsS -b /tmp/minimalrouter.cookies \
    https://192.168.1.1:8443/api/v1/system \
    >/tmp/system.json
jq -e '.lan_ip == "192.168.1.1" and .wan_enabled == false' /tmp/system.json

curl -kfsS https://192.168.1.1:8443/ | grep -q '<div id="root">'
nft list table inet minimalrouter >/tmp/nftables.txt
grep -q 'policy drop' /tmp/nftables.txt
grep -q 'iifname "eth1" udp dport { 53, 67 } accept' /tmp/nftables.txt
! grep -q 'udp dport 51820 accept' /tmp/nftables.txt

test -f /run/minimalrouter/dnsmasq.leases
rc-service dnsmasq status >/dev/null

trap - EXIT INT TERM
kill "$ROUTERD_PID" "$APPLYD_PID" >/dev/null 2>&1 || true
SMOKE
