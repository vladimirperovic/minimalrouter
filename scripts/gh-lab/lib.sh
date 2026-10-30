#!/bin/bash
# Shared settings and helpers for the GitHub lab. Source it; do not run it.
# (scripts/lab/ is the separate Proxmox torture lab; this one needs only a
# GitHub runner.)
#
# Topology (one Ubuntu runner, no hardware):
#
#   netns "isp"      PPPoE server (rp-pppoe), HTTP marker server, uplink NAT
#        | isp0 --- isp-br
#     [br-wan] --- tap-wan --- virtio NIC  \
#                                           router VM (the installed Golden image)
#     [br-lan] --- tap-lan --- e1000 NIC   /
#        |  host 192.168.1.50 (management)
#        +-- cl-br --- cl0   netns "client" (DHCP client behind the router)
#
# All values below are lab test values; none is a secret.

LAB_DIR=${LAB_DIR:-/tmp/minimalrouter-lab}
export LAB_RUN="$LAB_DIR/run"
LOGS="$LAB_DIR/logs"
mkdir -p "$LAB_RUN" "$LOGS"

ROUTER_LAN_IP=192.168.1.1
HOST_LAN_IP=192.168.1.50
ROUTER_PPP_IP=10.250.0.50
ISP_PPP_IP=10.250.0.1
# A public-style address on the provider side. The router drops forwarded replies from
# private sources (10/8 and friends) on its WAN links, so traffic from a LAN client
# can only be tested against a non-private "internet" address, never 10.250.0.1.
INET_IP=11.255.0.2
DASH_PASS='MinimalRouterLab-2026!'
ROOT_PASS='MRlabRecovery-2026!'
PPPOE_USER=mrlab
PPPOE_PASS='mrlab-pppoe-pass'
export DASH_PASS ROUTER_URL="https://$ROUTER_LAN_IP:8443"

log() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" | tee -a "$LOGS/lab.log" >&2; }

# rsh COMMAND... : run a command on the router over its real SSH service.
rsh() {
    sshpass -p "$ROOT_PASS" ssh \
        -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
        -o ConnectTimeout=5 -o ServerAliveInterval=5 -o ServerAliveCountMax=3 \
        "root@$ROUTER_LAN_IP" "$@"
}

isp() { sudo ip netns exec isp "$@"; }
client() { sudo ip netns exec client "$@"; }
api() { python3 "$(dirname "${BASH_SOURCE[0]}")/labapi.py" "$@"; }

# wait_for SECONDS COMMAND... : retry until the command succeeds.
wait_for() {
    local limit=$1
    shift
    local end=$((SECONDS + limit))
    until "$@" >/dev/null 2>&1; do
        [ "$SECONDS" -lt "$end" ] || return 1
        sleep 2
    done
}

# qemu_monitor COMMAND : send one human-monitor command to the router VM.
qemu_monitor() {
    printf '%s\n' "$1" | socat - "UNIX-CONNECT:$LAB_RUN/router.mon" >/dev/null
}

# Send the observer on stdin so its source never becomes a candidate process's
# command line. The observer anchors argv[0], requires one match and records
# starttime as well as PID, so PID reuse cannot hide a restart during a soak.
router_identity() {
    rsh sh -s "$1" < "$(dirname "${BASH_SOURCE[0]}")/process-identity.sh"
}

# router_pid NAME : only the daemon, never an SSH shell or supervisor.
router_pid() {
    local identity
    identity=$(router_identity "$1") || return 1
    printf '%s\n' "${identity%% *}"
}

# A socket pathname and routerd.ready can outlive SIGKILL. Require the listener
# itself to belong to the observed daemon, not just a stale filesystem entry.
router_listener_ready() {
    local name=$1 pid=$2
    case "$name" in routerd|router-applyd) ;; *) return 1 ;; esac
    [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 1
    rsh sh -s "$name" "$pid" <<'SH'
name=$1
pid=$2
test -s /run/minimalrouter/routerd.ready || exit 1
rc-service "$name" status >/dev/null 2>&1 || exit 1
case "$name" in
    routerd) family=-t; endpoint=8443 ;;
    router-applyd)
        test -S /run/minimalrouter/apply.sock || exit 1
        family=-x; endpoint=/run/minimalrouter/apply.sock ;;
    *) exit 1 ;;
esac
listeners=$(ss -H -l -n -p "$family") || exit 1
printf '%s\n' "$listeners" | awk -v endpoint="$endpoint" -v pid="$pid" '
    {
        # UNIX includes a Netid column; TCP-only output normally does not.
        state = ($1 == "LISTEN" ? 1 : ($2 == "LISTEN" ? 2 : 0))
        if (!state) next
        address = $(state + 3)
        matches = (endpoint == "8443" ? address ~ /:8443$/ : address == endpoint)
        if (matches && index($0, "pid=" pid ",")) found = 1
    }
    END { exit !found }
'
SH
}

# router_restarted_ready NAME OLD_IDENTITY : print a new PID/starttime only
# after it owns its listener and the management API answers. Bracket readiness
# with identity reads so another restart during the probe cannot count as ready.
router_restarted_ready() {
    local name=$1 old=$2 identity final code
    [[ "$old" =~ ^[1-9][0-9]*\ [0-9]+$ ]] || return 1
    identity=$(router_identity "$name") || return 1
    [[ "$identity" =~ ^[1-9][0-9]*\ [0-9]+$ ]] || return 1
    [ "$identity" != "$old" ] || return 1
    router_listener_ready "$name" "${identity%% *}" >/dev/null 2>&1 || return 1
    # This trusted-LAN read needs no login and cannot consume the auth budget.
    code=$(curl -sk -o /dev/null --max-time 5 -w '%{http_code}' "$ROUTER_URL/api/v1/setup/status") || return 1
    [ "$code" = 200 ] || return 1
    final=$(router_identity "$name") || return 1
    [ "$final" = "$identity" ] || return 1
    printf '%s\n' "$identity"
}

# Like wait_for, with the same two-second polling/deadline semantics, but keep
# the successful identity as evidence instead of discarding the probe output.
wait_for_router_restart() {
    local limit=$1 name=$2 old=$3 identity end
    [[ "$limit" =~ ^(0|[1-9][0-9]*)$ ]] || return 1
    case "$name" in routerd|router-applyd) ;; *) return 1 ;; esac
    [[ "$old" =~ ^[1-9][0-9]*\ [0-9]+$ ]] || return 1
    end=$((SECONDS + limit))
    while :; do
        if identity=$(router_restarted_ready "$name" "$old"); then
            printf '%s\n' "$identity"
            return 0
        fi
        if [ "$SECONDS" -ge "$end" ]; then
            echo "$name did not obtain a new live identity and ready listener within ${limit}s (old $old)" >&2
            return 1
        fi
        sleep 2
    done
}

# Save the complete stateless JSON, retaining sets and every rule. No remote
# shell pipeline: an SSH/nft error must not become the hash of an empty string.
firewall_snapshot() {
    local path=$1
    rsh 'nft -s -j list ruleset' > "$path" || return 1
    python3 "$(dirname "${BASH_SOURCE[0]}")/firewall_observer.py" "$path"
}

firewall_fingerprint() {
    local path
    path=$(mktemp "$LOGS/firewall-XXXXXX") || return 1
    firewall_snapshot "$path"
}
