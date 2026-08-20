#!/bin/sh
# GitHub-hosted QEMU transport overrides for the existing torture lab.
# This file is appended to a shadow copy of lib.sh by github-qemu-lab.sh, so
# every existing scenario keeps using the same checks while transport moves
# from Proxmox/qm guest-exec to ordinary QEMU + SSH.

: "${LAB_GITHUB_STATE:?LAB_GITHUB_STATE must point at the GitHub lab state directory}"
LAB_GITHUB_STATE="$(cd "$LAB_GITHUB_STATE" && pwd)"
LAB_GITHUB_KEY="${LAB_GITHUB_KEY:-$LAB_GITHUB_STATE/id_ed25519}"
LAB_RECOVERY_PW="${LAB_RECOVERY_PW:-MRciRecovery-2026!}"
LAB_ADMIN_PW="${LAB_ADMIN_PW:-MinimalRouterCI-2026!}"
ADMIN_PW="$LAB_ADMIN_PW"

ISP_VMID=150
MR_VMID=151
SIM_VMID=153
LAN_VMID=154
MR_LAN_IF=eth1
MR_WAN_IF=eth0

_gh_aux_port() {
  case "$1" in
    150) echo 2250 ;;
    153) echo 2253 ;;
    154|152) echo 2254 ;;
    *) return 1 ;;
  esac
}

_gh_aux_exec() {
  vmid="$1"; cmd="$2"
  port="$(_gh_aux_port "$vmid")" || return 1
  b64="$(printf '%s' "$cmd" | base64 -w0 2>/dev/null || printf '%s' "$cmd" | base64 | tr -d '\n')"
  ssh -i "$LAB_GITHUB_KEY" \
    -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o LogLevel=ERROR -o ConnectTimeout=8 -p "$port" lab@127.0.0.1 \
    "echo '$b64' | base64 -d | sudo -n sh"
}

_gh_mr_exec() {
  cmd="$1"
  b64="$(printf '%s' "$cmd" | base64 -w0 2>/dev/null || printf '%s' "$cmd" | base64 | tr -d '\n')"
  sshpass -p "$LAB_RECOVERY_PW" ssh \
    -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o LogLevel=ERROR -o ConnectTimeout=8 \
    root@192.168.1.1 "echo '$b64' | base64 -d | sh"
}

# Host commands now run directly on the GitHub-hosted runner. A tiny qm shim
# in $LAB_GITHUB_STATE/bin provides start/stop/reboot/status semantics used by
# the power-loss/reboot scenarios.
H() {
  LAB_GITHUB_STATE="$LAB_GITHUB_STATE" \
  LAB_GITHUB_KEY="$LAB_GITHUB_KEY" \
  LAB_RECOVERY_PW="$LAB_RECOVERY_PW" \
  PATH="$LAB_GITHUB_STATE/bin:$PATH" \
  sh -c "$*"
}

gx() {
  vmid="$1"; cmd="$2"
  case "$vmid" in
    151) _gh_mr_exec "$cmd" ;;
    150|152|153|154) _gh_aux_exec "$vmid" "$cmd" ;;
    *) echo "github backend: unsupported VMID $vmid" >&2; return 1 ;;
  esac
}

ispfault() { gx 150 "lab-fault $* 2>&1"; }
isp() { gx 150 "$* 2>&1"; }
sim() { gx 153 "$* 2>&1"; }
lan() { gx 154 "$* 2>&1"; }
mr() { gx 151 "$* 2>&1"; }

# Hosted runners are disposable, so the physical-host thermal guard and
# production pfSense invariant become an isolation invariant: the test may
# touch only the four lab bridges/taps and must not change the runner's default
# route.
temp_guard() { return 0; }

prod_ports_fingerprint() {
  {
    ip -o link show | awk -F': ' '$2 ~ /^(br-lab-|tap-)/ {print $2}' | sort
    printf 'default=%s\n' "$(ip route show default | head -1)"
  } | sha256sum | awk '{print $1}'
}
prod_ports_md5() { prod_ports_fingerprint; }

check_prod_untouched() {
  before="$1"
  [ "$(prod_ports_fingerprint)" = "$before" ] || return 1
  expected="$(cat "$LAB_GITHUB_STATE/default-route" 2>/dev/null || true)"
  [ -n "$expected" ] && [ "$(ip route show default | head -1)" = "$expected" ]
}

assert_lab_topology() {
  for bridge in br-lab-wan br-lab-lan br-lab-extra; do
    ip link show "$bridge" >/dev/null 2>&1 || {
      echo "missing GitHub lab bridge: $bridge" >&2; return 1;
    }
  done
  for vm in 150 151 153 154; do
    PATH="$LAB_GITHUB_STATE/bin:$PATH" LAB_GITHUB_STATE="$LAB_GITHUB_STATE" qm status "$vm" 2>/dev/null | grep -qi running || {
      echo "GitHub QEMU VM $vm is not running" >&2; return 1;
    }
  done
  [ "$(ip route show default | head -1)" = "$(cat "$LAB_GITHUB_STATE/default-route")" ] || {
    echo "runner default route changed" >&2; return 1;
  }
}

wait_vm_stopped() {
  vmid="$1"; t="${2:-120}"; i=0
  while [ "$i" -lt "$t" ]; do
    PATH="$LAB_GITHUB_STATE/bin:$PATH" LAB_GITHUB_STATE="$LAB_GITHUB_STATE" qm status "$vmid" 2>/dev/null | grep -qi stopped && return 0
    sleep 2; i=$((i+2))
  done
  return 1
}
wait_vm_running() {
  vmid="$1"; t="${2:-120}"; i=0
  while [ "$i" -lt "$t" ]; do
    PATH="$LAB_GITHUB_STATE/bin:$PATH" LAB_GITHUB_STATE="$LAB_GITHUB_STATE" qm status "$vmid" 2>/dev/null | grep -qi running && return 0
    sleep 2; i=$((i+2))
  done
  return 1
}
