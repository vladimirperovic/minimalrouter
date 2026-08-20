#!/usr/bin/env bash
# Minimal Proxmox-qm compatibility shim for the GitHub-hosted QEMU lab.
set -euo pipefail
: "${LAB_GITHUB_STATE:?}"
state="$LAB_GITHUB_STATE"
cmd="${1:-}"; shift || true
vm="${1:-}"

pidfile() { echo "$state/vm-$1.pid"; }
startfile() { echo "$state/start-$1.sh"; }
running() {
  local pf pid
  pf="$(pidfile "$1")"
  [[ -s "$pf" ]] || return 1
  pid="$(cat "$pf")"
  kill -0 "$pid" 2>/dev/null
}

aux_port() {
  case "$1" in
    150) echo 2250;;
    153) echo 2253;;
    154|152) echo 2254;;
    *) return 1;;
  esac
}

wait_ssh() {
  local v="$1" timeout="${2:-180}" i=0 port
  while (( i < timeout )); do
    if [[ "$v" == 151 ]]; then
      if sshpass -p "${LAB_RECOVERY_PW:-MRciRecovery-2026!}" ssh \
        -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
        -o ConnectTimeout=2 root@192.168.1.1 true >/dev/null 2>&1; then return 0; fi
    else
      port="$(aux_port "$v")"
      if ssh -i "${LAB_GITHUB_KEY:-$state/id_ed25519}" \
        -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o LogLevel=ERROR -o ConnectTimeout=2 -p "$port" lab@127.0.0.1 true >/dev/null 2>&1; then return 0; fi
    fi
    sleep 2; i=$((i+2))
  done
  return 1
}

start_vm() {
  local v="$1"
  running "$v" && return 0
  rm -f "$(pidfile "$v")"
  [[ -x "$(startfile "$v")" ]] || { echo "missing start script for VM $v" >&2; return 1; }
  "$(startfile "$v")"
  local i=0
  while (( i < 30 )); do running "$v" && return 0; sleep 1; i=$((i+1)); done
  return 1
}

stop_vm() {
  local v="$1" pf pid i=0
  pf="$(pidfile "$v")"
  running "$v" || return 0
  pid="$(cat "$pf")"
  kill -TERM "$pid" 2>/dev/null || true
  while (( i < 20 )); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 1; i=$((i+1))
  done
  kill -KILL "$pid" 2>/dev/null || true
}

case "$cmd" in
  status)
    [[ -n "$vm" ]] || exit 2
    if running "$vm"; then echo "status: running"; else echo "status: stopped"; fi
    ;;
  start)
    [[ -n "$vm" ]] || exit 2
    start_vm "$vm"
    ;;
  stop|shutdown)
    [[ -n "$vm" ]] || exit 2
    stop_vm "$vm"
    ;;
  reset)
    [[ -n "$vm" ]] || exit 2
    stop_vm "$vm"; start_vm "$vm"; wait_ssh "$vm" 240
    ;;
  reboot)
    [[ -n "$vm" ]] || exit 2
    if ! running "$vm"; then start_vm "$vm"; wait_ssh "$vm" 240; exit 0; fi
    if [[ "$vm" == 151 ]]; then
      sshpass -p "${LAB_RECOVERY_PW:-MRciRecovery-2026!}" ssh \
        -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
        -o ConnectTimeout=4 root@192.168.1.1 'reboot -f' >/dev/null 2>&1 || true
    else
      port="$(aux_port "$vm")"
      ssh -i "${LAB_GITHUB_KEY:-$state/id_ed25519}" \
        -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o LogLevel=ERROR -o ConnectTimeout=4 -p "$port" lab@127.0.0.1 \
        'sudo -n reboot -f' >/dev/null 2>&1 || true
    fi
    sleep 3
    wait_ssh "$vm" 300
    ;;
  wait)
    # Proxmox callers in this lab use qm wait as a reboot synchronization point.
    # If the VM is running, wait for its management shell; if it is stopped,
    # returning success matches the stopped-state synchronization use case.
    [[ -n "$vm" ]] || exit 2
    running "$vm" && wait_ssh "$vm" 300 || true
    ;;
  config)
    [[ -n "$vm" ]] || exit 2
    echo "name: github-qemu-$vm"
    echo "onboot: 0"
    case "$vm" in
      151) echo 'net0: virtio,bridge=br-lab-wan'; echo 'net1: virtio,bridge=br-lab-lan'; echo 'net2: virtio,bridge=br-lab-extra';;
      150) echo 'net1: virtio,bridge=br-lab-wan';;
      153) echo 'net1: virtio,bridge=br-lab-wan'; echo 'net2: virtio,bridge=br-lab-extra';;
      154|152) echo 'net1: virtio,bridge=br-lab-lan';;
    esac
    ;;
  *)
    echo "github qm shim: unsupported command '$cmd'" >&2
    exit 2
    ;;
esac
