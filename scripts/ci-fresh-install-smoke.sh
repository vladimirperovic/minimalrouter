#!/bin/sh
set -eu

ARTIFACT="${1:-build/minimalrouter-linux-amd64.tar.gz}"
[ -f "$ARTIFACT" ] || {
    echo "Missing distribution artifact: $ARTIFACT" >&2
    exit 1
}

BUILD_DIR="$(cd "$(dirname "$ARTIFACT")" && pwd)"
ARTIFACT_NAME="$(basename "$ARTIFACT")"
FIXTURE_DIR="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_DIR"' EXIT INT TERM
go run scripts/render-policy-fixture.go "$FIXTURE_DIR"

docker run --rm --privileged \
    -v "$BUILD_DIR:/artifacts:ro" \
    -v "$FIXTURE_DIR:/fixtures:ro" \
    -v /lib/modules:/lib/modules:ro \
    -e "ARTIFACT_NAME=$ARTIFACT_NAME" \
    alpine:3.22 sh -euxs <<'SMOKE'
apk add --no-cache curl jq iproute2

nft -c -f /fixtures/iot-policy.nft
dnsmasq --test --conf-file=/fixtures/iot-policy.dnsmasq
grep -q 'meta day { "Monday", "Tuesday", "Wednesday", "Thursday", "Friday" } meta hour "19:00:00"-"23:59:59"' /fixtures/iot-policy.nft
grep -q 'iifname "mr-iot" oifname "eth1" drop' /fixtures/iot-policy.nft

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
test "$(sysctl -n net.ipv4.ip_forward)" = "1"
rc-update show default | grep -q chronyd

if ! ip link show eth1 >/dev/null 2>&1; then
    ip link add eth1 type dummy
fi
ip link set eth1 up
if ! ip link show eth2 >/dev/null 2>&1; then
    ip link add eth2 type dummy
fi
ip link set eth2 up

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
        "lan_ip_address": "192.168.1.1",
        "timezone": "UTC"
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
    .wifi.enabled == false and
    .iot.enabled == false and
    .device_policies.enabled == false and
    .system.timezone == "UTC"
' /tmp/config.json

CSRF="$(jq -r '.csrf_token' /tmp/setup-result.json)"
jq '
  .iot.enabled = true |
  .iot.mode = "dedicated" |
  .iot.interface = "eth2" |
  .iot.parent_interface = "" |
  .iot.vlan_id = 0 |
  .iot.dhcp.static_leases = [{"id":"camera","hostname":"camera","mac":"02:00:00:00:30:10","ip_address":"192.168.30.50"}] |
  .dhcp.static_leases = [{"id":"kids-tablet","hostname":"kids-tablet","mac":"02:00:00:00:00:10","ip_address":"192.168.1.50"}] |
  .device_policies.enabled = true |
  .device_policies.profiles = [{
    "id":"kids-evening",
    "name":"Kids evening",
    "enabled":true,
    "access_mode":"allow_services",
    "allowed_services":["youtube","steam"],
    "windows":[
      {"days":["monday","tuesday","wednesday","thursday","friday"],"start":"19:00","end":"23:59","all_day":false},
      {"days":["saturday","sunday"],"all_day":true}
    ]
  }] |
  .device_policies.assignments = [{
    "id":"kids-tablet",
    "hostname":"kids-tablet",
    "mac":"02:00:00:00:00:10",
    "ip_address":"192.168.1.50",
    "zone":"lan",
    "profile_id":"kids-evening"
  }]
' /tmp/config.json >/tmp/iot-config.json

curl -kfsS -b /tmp/minimalrouter.cookies \
    -H "X-CSRF-Token: $CSRF" \
    -H "Content-Type: application/json" \
    --data-binary @/tmp/iot-config.json \
    https://192.168.1.1:8443/api/v1/config \
    >/tmp/iot-result.json
jq -e '.state == "AwaitingConfirmation" and (.id | length) > 0' /tmp/iot-result.json
TX_ID="$(jq -r '.id' /tmp/iot-result.json)"
curl -kfsS -b /tmp/minimalrouter.cookies \
    -H "X-CSRF-Token: $CSRF" \
    -X POST \
    "https://192.168.1.1:8443/api/v1/transactions/${TX_ID}/confirm" \
    >/tmp/iot-confirm.json
jq -e '.state == "Committed" or .success == true' /tmp/iot-confirm.json

ip -4 address show dev eth2 | grep -q '192.168.30.1/24'
grep -q 'interface=eth2' /etc/dnsmasq.d/minimalrouter.conf
grep -q 'dhcp-range=set:iot,192.168.30.100,192.168.30.200,255.255.255.0,12h' /etc/dnsmasq.d/minimalrouter.conf
nft list table inet minimalrouter >/tmp/iot-nftables.txt
grep -q 'iifname "eth2" oifname "eth1" drop' /tmp/iot-nftables.txt
grep -q 'iifname "eth1" oifname "eth2" drop' /tmp/iot-nftables.txt
grep -q 'set svc_youtube' /tmp/iot-nftables.txt
grep -q 'set svc_steam' /tmp/iot-nftables.txt
grep -q 'iifname "eth1" ip saddr 192.168.1.50 drop' /tmp/iot-nftables.txt

curl -kfsS -b /tmp/minimalrouter.cookies \
    https://192.168.1.1:8443/api/v1/system \
    >/tmp/system.json
jq -e '.lan_ip == "192.168.1.1" and .wan_enabled == false' /tmp/system.json

curl -kfsS https://192.168.1.1:8443/ | grep -q '<div id="root">'
nft list table inet minimalrouter >/tmp/nftables.txt
grep -q 'policy drop' /tmp/nftables.txt
grep -q 'iifname "eth1" udp dport { 53, 67 } accept' /tmp/nftables.txt
! grep -q 'udp dport 51820 accept' /tmp/nftables.txt

test "$(sysctl -n net.ipv4.ip_forward)" = "1"
dnsmasq --test --conf-file=/etc/dnsmasq.d/minimalrouter.conf
rc-service dnsmasq status >/dev/null

trap - EXIT INT TERM
kill "$ROUTERD_PID" "$APPLYD_PID" >/dev/null 2>&1 || true
SMOKE

trap - EXIT INT TERM
rm -rf "$FIXTURE_DIR"
