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

# router_pid NAME : pid of the daemon from the active slot (the supervisor's
# command line also mentions the daemon, so match the slot binary).
router_pid() {
    rsh "pgrep -f 'slots/.*/bin/$1-amd64' | head -n 1"
}
