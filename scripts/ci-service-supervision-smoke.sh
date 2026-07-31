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
    -v /lib/modules:/lib/modules:ro \
    -e "ARTIFACT_NAME=$ARTIFACT_NAME" \
    alpine:3.22 sh -euxs <<'SMOKE'
apk add --no-cache curl iproute2 jq

mkdir -p /tmp/minimalrouter-dist
tar xzf "/artifacts/$ARTIFACT_NAME" -C /tmp/minimalrouter-dist
DIST_DIR="$(find /tmp/minimalrouter-dist -mindepth 1 -maxdepth 1 -type d | head -1)"
[ -n "$DIST_DIR" ]
cd "$DIST_DIR"
sh install.sh

for service in routerd router-applyd; do
    init="/etc/init.d/$service"
    grep -q '^supervisor="supervise-daemon"$' "$init"
    grep -q '^respawn_delay=2$' "$init"
    grep -q '^respawn_max=5$' "$init"
    grep -q '^respawn_period=60$' "$init"
done

if ! ip link show eth1 >/dev/null 2>&1; then
    ip link add eth1 type dummy
fi
ip link set eth1 up

cleanup() {
    status=$?
    rc-service routerd stop >/dev/null 2>&1 || true
    rc-service router-applyd stop >/dev/null 2>&1 || true
    if [ "$status" -ne 0 ]; then
        echo "===== routerd log =====" >&2
        cat /var/log/routerd.log /var/log/routerd.err >&2 2>/dev/null || true
        echo "===== router-applyd log =====" >&2
        cat /var/log/router-applyd.log /var/log/router-applyd.err >&2 2>/dev/null || true
        echo "===== OpenRC status =====" >&2
        rc-status -a >&2 || true
        echo "===== processes =====" >&2
        ps -ef >&2 || true
    fi
    exit "$status"
}
trap cleanup EXIT INT TERM

child_pid() {
    pidfile="$1"
    [ -s "$pidfile" ] || return 1
    supervisor_pid="$(cat "$pidfile")"
    [ -r "/proc/$supervisor_pid/task/$supervisor_pid/children" ] || return 1
    children="$(cat "/proc/$supervisor_pid/task/$supervisor_pid/children")"
    set -- $children
    [ "$#" -eq 1 ] || return 1
    kill -0 "$1" >/dev/null 2>&1 || return 1
    printf '%s\n' "$1"
}

wait_child() {
    pidfile="$1"
    old_pid="${2:-}"
    for _ in $(seq 1 80); do
        current="$(child_pid "$pidfile" 2>/dev/null || true)"
        if [ -n "$current" ] && [ "$current" != "$old_pid" ]; then
            printf '%s\n' "$current"
            return 0
        fi
        sleep 0.25
    done
    return 1
}

wait_socket() {
    for _ in $(seq 1 120); do
        [ -S /run/minimalrouter/apply.sock ] && return 0
        sleep 0.25
    done
    return 1
}

wait_setup() {
    for _ in $(seq 1 90); do
        if curl -kfsS https://192.168.1.1:8443/api/v1/setup/status >/tmp/setup-status.json 2>/dev/null; then
            return 0
        fi
        sleep 1
    done
    return 1
}

rc-service router-applyd start
rc-service routerd start
wait_socket
wait_setup

APPLY_PIDFILE=/run/router-applyd.supervisor.pid
ROUTERD_PIDFILE=/run/routerd.supervisor.pid
apply_before="$(wait_child "$APPLY_PIDFILE")"
routerd_before="$(wait_child "$ROUTERD_PIDFILE")"

# A pre-configuration helper crash must respawn cleanly and keep first-run setup
# available. The fixed delay and bounded crash window prevent an infinite loop.
kill -KILL "$apply_before"
apply_after="$(wait_child "$APPLY_PIDFILE" "$apply_before")"
[ "$apply_after" != "$apply_before" ]
wait_socket
wait_setup

curl -kfsS \
    -c /tmp/minimalrouter.cookies \
    -H 'Content-Type: application/json' \
    --data '{
        "wan_interface": "eth0",
        "lan_interface": "eth1",
        "pppoe_username": "",
        "pppoe_password": "",
        "admin_password": "service-supervision-admin-password-123!",
        "lan_ip_address": "192.168.1.1"
    }' \
    https://192.168.1.1:8443/api/v1/setup/apply \
    >/tmp/setup-result.json
jq -e '.success == true' /tmp/setup-result.json

# After canonical state exists, a helper respawn must restore its protected IPC
# endpoint without disturbing the already verified forwarding state.
apply_before="$apply_after"
kill -KILL "$apply_before"
apply_after="$(wait_child "$APPLY_PIDFILE" "$apply_before")"
[ "$apply_after" != "$apply_before" ]
wait_socket
nft list table inet minimalrouter >/dev/null
curl -kfsS -b /tmp/minimalrouter.cookies \
    https://192.168.1.1:8443/api/v1/config \
    | jq -e '.lan.interface == "eth1" and .lan.ip_address == "192.168.1.1"'

# The unprivileged management process must also respawn, preserving its durable
# session state and returning to authenticated service on a different child PID.
kill -KILL "$routerd_before"
routerd_after="$(wait_child "$ROUTERD_PIDFILE" "$routerd_before")"
[ "$routerd_after" != "$routerd_before" ]
wait_setup
curl -kfsS -b /tmp/minimalrouter.cookies \
    https://192.168.1.1:8443/api/v1/config \
    | jq -e '.revision >= 1'

rc-service routerd stop
rc-service router-applyd stop
trap - EXIT INT TERM
SMOKE
