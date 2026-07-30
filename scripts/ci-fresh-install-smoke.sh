#!/bin/sh
set -eu

ARTIFACT="${1:-build/minimalrouter-linux-amd64.tar.gz}"
[ -f "$ARTIFACT" ] || {
    echo "Missing distribution artifact: $ARTIFACT" >&2
    exit 1
}
[ -d build/dist/minimalrouter-linux-amd64 ] || {
    echo "Missing extracted AMD64 distribution tree" >&2
    exit 1
}

BUILD_DIR="$(cd "$(dirname "$ARTIFACT")" && pwd)"
SMOKE_ROOT="$BUILD_DIR/update-smoke"
rm -rf "$SMOKE_ROOT"
mkdir -p "$SMOKE_ROOT"
PRIVATE_KEY="$SMOKE_ROOT/release.key"
PUBLIC_KEY="$SMOKE_ROOT/release.pub"

go run ./cmd/firmware-keygen --private-key "$PRIVATE_KEY" --public-key "$PUBLIC_KEY"

prepare_release() {
    version="$1"
    marker="$2"
    destination="$SMOKE_ROOT/release-$version"
    manifest="$SMOKE_ROOT/release-$version.manifest.json"
    cp -R build/dist/minimalrouter-linux-amd64 "$destination"
    printf '%s\n' "$marker" > "$destination/web/dist/update-marker.txt"

    # A broken updater or recovery binary in the candidate slot must never be
    # able to disable local rollback/recovery. The stable dispatcher must keep
    # these two commands on the independently installed bootstrap payload.
    cat > "$destination/bin/router-update-amd64" <<'BROKEN_UPDATE'
#!/bin/sh
echo "ERROR: active-slot router-update was executed" >&2
exit 99
BROKEN_UPDATE
    cat > "$destination/bin/router-recovery-amd64" <<'BROKEN_RECOVERY'
#!/bin/sh
echo "ERROR: active-slot router-recovery was executed" >&2
exit 98
BROKEN_RECOVERY
    chmod 0755 \
        "$destination/bin/router-update-amd64" \
        "$destination/bin/router-recovery-amd64"

    go run ./cmd/firmware-sign \
        --dir "$destination" \
        --key "$PRIVATE_KEY" \
        --version "$version" \
        --commit "ci-update-smoke" \
        --public-key-output "$destination/firmware-signing.pub" \
        --output "$manifest"
}

# The install archive carries the same pinned key as the update payloads. The
# manifest is retained only as evidence that the installer payload is signable.
INSTALL_DIR="$SMOKE_ROOT/install/minimalrouter-linux-amd64"
mkdir -p "$(dirname "$INSTALL_DIR")"
cp -R build/dist/minimalrouter-linux-amd64 "$INSTALL_DIR"
go run ./cmd/firmware-sign \
    --dir "$INSTALL_DIR" \
    --key "$PRIVATE_KEY" \
    --version "9.9.7" \
    --commit "ci-install-smoke" \
    --public-key-output "$INSTALL_DIR/firmware-signing.pub" \
    --output "$SMOKE_ROOT/install.manifest.json"
tar czf "$SMOKE_ROOT/minimalrouter-ci-install.tar.gz" -C "$SMOKE_ROOT/install" minimalrouter-linux-amd64
sh scripts/checksum-file.sh \
    "$SMOKE_ROOT/minimalrouter-ci-install.tar.gz" \
    "$SMOKE_ROOT/minimalrouter-ci-install.tar.gz.sha256"

prepare_release "9.9.8" "slot-one"
prepare_release "9.9.9" "slot-two"
rm -f "$PRIVATE_KEY"

ARTIFACT_REL="update-smoke/minimalrouter-ci-install.tar.gz"

docker run --rm --privileged \
    -v "$BUILD_DIR:/artifacts:ro" \
    -v /lib/modules:/lib/modules:ro \
    -e "ARTIFACT_REL=$ARTIFACT_REL" \
    alpine:3.22 sh -euxs <<'SMOKE'
apk add --no-cache curl jq iproute2

cd /artifacts/update-smoke
sha256sum -c minimalrouter-ci-install.tar.gz.sha256

mkdir -p /tmp/minimalrouter-dist
tar xzf "/artifacts/$ARTIFACT_REL" -C /tmp/minimalrouter-dist
DIST_DIR="$(find /tmp/minimalrouter-dist -mindepth 1 -maxdepth 1 -type d | head -1)"
[ -n "$DIST_DIR" ]
cd "$DIST_DIR"

sh -n install.sh
sh -n slot-exec
sh install.sh

test -L /usr/bin/routerd
test -L /usr/sbin/router-applyd
test -L /usr/sbin/router-recovery
test -L /usr/sbin/router-update
test -x /usr/libexec/minimalrouter/slot-exec
test -x /usr/libexec/minimalrouter/bootstrap/bin/routerd-amd64
test -x /usr/libexec/minimalrouter/bootstrap/bin/router-applyd-amd64
test -f /usr/libexec/minimalrouter/bootstrap/web/dist/index.html
test -f /usr/share/minimalrouter/web/index.html
test -f /etc/minimalrouter/firmware-signing.pub
cmp -s /etc/minimalrouter/firmware-signing.pub /artifacts/update-smoke/release.pub

test "$(stat -c %a /var/lib/minimalrouter)" = "700"
test "$(stat -c %a /var/lib/minimalrouter-applyd)" = "700"
test "$(stat -c %a /var/lib/minimalrouter-update)" = "755"
test "$(sysctl -n net.ipv4.ip_forward)" = "1"

MINIMALROUTER_DATA_DIR=/tmp/recovery-help-state /usr/sbin/router-recovery --help >/tmp/recovery-help.txt
grep -q 'Usage: router-recovery' /tmp/recovery-help.txt
test ! -e /tmp/recovery-help-state
/usr/sbin/router-update --help >/tmp/update-help.txt
grep -q 'Usage: router-update' /tmp/update-help.txt
/usr/sbin/router-update status | jq -e '.current == "" and .previous == "" and .pending == ""'

if ! ip link show eth1 >/dev/null 2>&1; then
    ip link add eth1 type dummy
fi
ip link set eth1 up

ROUTERD_PID=""
APPLYD_PID=""
start_router() {
    /usr/sbin/router-applyd >/tmp/router-applyd.log 2>&1 &
    APPLYD_PID=$!
    /usr/bin/routerd >/tmp/routerd.log 2>&1 &
    ROUTERD_PID=$!
}
stop_router() {
    [ -z "$ROUTERD_PID" ] || kill "$ROUTERD_PID" >/dev/null 2>&1 || true
    [ -z "$APPLYD_PID" ] || kill "$APPLYD_PID" >/dev/null 2>&1 || true
    [ -z "$ROUTERD_PID" ] || wait "$ROUTERD_PID" >/dev/null 2>&1 || true
    [ -z "$APPLYD_PID" ] || wait "$APPLYD_PID" >/dev/null 2>&1 || true
    ROUTERD_PID=""
    APPLYD_PID=""
}
wait_router() {
    ready=0
    for _ in $(seq 1 90); do
        if curl -kfsS https://192.168.1.1:8443/api/v1/setup/status >/tmp/setup-status.json 2>/dev/null; then
            ready=1
            break
        fi
        sleep 1
    done
    [ "$ready" -eq 1 ]
}
cleanup() {
    status=$?
    stop_router
    if [ "$status" -ne 0 ]; then
        echo "===== router-applyd.log =====" >&2
        cat /tmp/router-applyd.log >&2 || true
        echo "===== routerd.log =====" >&2
        cat /tmp/routerd.log >&2 || true
        echo "===== update state =====" >&2
        /usr/sbin/router-update status >&2 || true
        find /var/lib/minimalrouter-update -maxdepth 4 -ls >&2 || true
        echo "===== nftables =====" >&2
        nft list ruleset >&2 || true
        echo "===== addresses =====" >&2
        ip -4 address >&2 || true
    fi
    exit "$status"
}
trap cleanup EXIT INT TERM

start_router
wait_router
jq -e '.is_configured == false and .lan_interface == "eth1" and .lan_ip == "192.168.1.1"' /tmp/setup-status.json

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

curl -kfsS -b /tmp/minimalrouter.cookies https://192.168.1.1:8443/api/v1/config >/tmp/config.json
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
curl -kfsS https://192.168.1.1:8443/ | grep -q '<div id="root">'
nft list table inet minimalrouter >/tmp/nftables.txt
grep -q 'policy drop' /tmp/nftables.txt
grep -q 'iifname "eth1" udp dport { 53, 67 } accept' /tmp/nftables.txt
! grep -q 'udp dport 51820 accept' /tmp/nftables.txt
dnsmasq --test --conf-file=/etc/dnsmasq.d/minimalrouter.conf

# Exercise the complete stable-command -> active-slot path twice, then rollback.
stop_router
/usr/sbin/router-update stage \
    --dir /artifacts/update-smoke/release-9.9.8 \
    --manifest /artifacts/update-smoke/release-9.9.8.manifest.json
/usr/sbin/router-update activate --version 9.9.8 --confirm ACTIVATE-UPDATE
test "$(readlink /var/lib/minimalrouter-update/current)" = "slots/9.9.8"
su routerd -s /bin/sh -c 'test -x /var/lib/minimalrouter-update/current/bin/routerd-amd64'
/usr/sbin/router-update status | jq -e '.current == "9.9.8" and .pending == ""'
/usr/sbin/router-recovery --help | grep -q 'Usage: router-recovery'
start_router
wait_router
curl -kfsS https://192.168.1.1:8443/update-marker.txt | grep -qx 'slot-one'

stop_router
/usr/sbin/router-update stage \
    --dir /artifacts/update-smoke/release-9.9.9 \
    --manifest /artifacts/update-smoke/release-9.9.9.manifest.json
/usr/sbin/router-update activate --version 9.9.9 --confirm ACTIVATE-UPDATE
/usr/sbin/router-update status | jq -e '.current == "9.9.9" and .previous == "9.9.8" and .pending == ""'
/usr/sbin/router-recovery --help | grep -q 'Usage: router-recovery'
start_router
wait_router
curl -kfsS https://192.168.1.1:8443/update-marker.txt | grep -qx 'slot-two'

stop_router
/usr/sbin/router-update rollback --confirm ROLLBACK-UPDATE
/usr/sbin/router-update status | jq -e '.current == "9.9.8" and .previous == "9.9.9" and .pending == ""'
start_router
wait_router
curl -kfsS https://192.168.1.1:8443/update-marker.txt | grep -qx 'slot-one'

trap - EXIT INT TERM
stop_router
SMOKE
