#!/bin/sh
# MinimalRouter torture-lab scenario runner.
#
# Each scenario follows the 8-phase protocol:
#   1. reset lab to known state         (lab-fault reset, PPPoE up, hooks disarmed)
#   2. confirm initial invariants       (firewall, LAN, PPPoE, DNS, production)
#   3. execute fault scenario
#   4. check MinimalRouter API + kernel runtime
#   5. check LAN client
#   6. revert the fault
#   7. verify automatic recovery
#   8. record PASS/FAIL + captured logs
#
# Usage:
#   sh lab-run.sh list
#   sh lab-run.sh <scenario> [scenario...]
#   sh lab-run.sh all            (run every scenario)
#   sh lab-run.sh net            (run every scenario matching *net*)

set -eu
LABDIR="$(cd "$(dirname "$0")" && pwd)"
SCENDIR="$LABDIR/scenarios"
LAB_RESULTS="${LAB_RESULTS:-$LABDIR/results}"
export LAB_RESULTS
. "$LABDIR/lib.sh"

log() { echo "[$(date +%H:%M:%S)] $*"; }

reset_lab() {
  log "=== reset lab to known state ==="
  ispfault reset >/dev/null 2>&1 || true
  disarm_hooks >/dev/null 2>&1 || true
  # make sure PPPoE is negotiated before invariants
  if ! wait_pppoe 60; then
    log "PPPoE not up after reset; bouncing pppoe-wan on MR"
    mr 'rc-service pppoe-wan restart 2>/dev/null || true' >/dev/null 2>&1
    wait_pppoe 60 || true
  fi
  # canonical feature flags may leak from failed scenarios; normalize baseline
  if reset_mr_baseline >/dev/null 2>&1; then
    log "canonical feature baseline reset (adguard/squid/qos off)"
  else
    log "canonical baseline reset skipped/failed (continuing)"
  fi
  # production baseline snapshot (bridge ports must never change)
  PROD_PORTS_BEFORE="$(prod_ports_fingerprint)"
  export PROD_PORTS_BEFORE
  log "production vmbr0 port fingerprint: $PROD_PORTS_BEFORE"
}

initial_invariants() {
  log "=== initial invariants ==="
  check_fw_not_fail_open && log "  firewall policy drop: OK" || log "  firewall policy drop: FAIL (continuing)"
  check_pppoe && log "  PPPoE $MR_WAN_PPP: OK" || log "  PPPoE: FAIL"
  check_lan_up && log "  LAN reachable: OK" || log "  LAN: FAIL"
  check_local_dns && log "  local DNS: OK" || log "  local DNS: FAIL"
  check_lan_internet && log "  LAN->internet: OK" || log "  LAN->internet: FAIL"
}

preflight_lab() {
  log "=== safety preflight: isolated lab topology ==="
  assert_lab_topology
  log "  targets: ISP=$ISP_VMID MR=$MR_VMID SIM=$SIM_VMID LAN=$LAN_VMID"
  log "  production: pfSense 106 + vmbr0 (read-only invariant only)"
}

# Best-effort recovery for faults that outlive an abruptly failed scenario.
# Targets are deliberately explicit lab paths/services; this never touches a
# production VM, bridge, or broad filesystem location.
recover_failed_scenario() {
  log "=== emergency lab cleanup after failure ==="
  ispfault reset >/dev/null 2>&1 || true
  disarm_hooks >/dev/null 2>&1 || true
  isp "rm -f /etc/dnsmasq.d/rebind.conf; sed -i 's/10\\.250\\.0\\.99$/10.250.0.50/' /etc/ppp/chap-secrets /etc/ppp/pap-secrets 2>/dev/null || true; systemctl restart dnsmasq pppoe-server" >/dev/null 2>&1 || true
  sim "systemctl start wg-quick@wg1 2>/dev/null || true; rm -f /tmp/lab-wg.psk" >/dev/null 2>&1 || true
  mr "mount -o remount,rw / 2>/dev/null || true; mountpoint -q /var/lib/minimalrouter-applyd && umount /var/lib/minimalrouter-applyd || true; rm -f /root/.lab-enospc-fill /root/.lab-enospc-tail /root/.lab-last-good.json; if [ -f /run/lab-cpu-load.pids ]; then while read p; do kill \"\$p\" 2>/dev/null || true; done </run/lab-cpu-load.pids; rm -f /run/lab-cpu-load.pids; fi" >/dev/null 2>&1 || true
}

run_scenario() {
  name="$1"
  file="$SCENDIR/$name.sh"
  [ -f "$file" ] || { echo "unknown scenario: $name (see: sh lab-run.sh list)"; return 1; }
  log ""
  log "############################################"
  log "# SCENARIO: $name"
  log "############################################"
  reset_lab
  initial_invariants
  log "=== executing scenario ==="
  if ! sh "$file"; then
    log "scenario $name FAILED (captured state below)"
    CURRENT_SCENARIO="$name"
    CURRENT_PHASE="postmortem"
    mkdir -p "$RESULTS_DIR/$CURRENT_SCENARIO"
    capture_state "postmortem"
    recover_failed_scenario
    return 1
  fi
  log "scenario $name PASS"
  return 0
}

case "${1:-}" in
  list)
    echo "Scenarios:"
    for f in $(find "$SCENDIR" -maxdepth 1 -type f -name '[0-9]*.sh' | sort -V); do
      b="$(basename "$f" .sh)"
      desc="$(sed -n 's/^# //p' "$f" | head -1)"
      printf "  %-32s %s\n" "$b" "$desc"
    done
    exit 0
    ;;
  "")
    echo "usage: sh lab-run.sh list | <scenario>... | all"; exit 1
    ;;
esac

if [ "$1" = all ]; then
  preflight_lab
  set -- $(find "$SCENDIR" -maxdepth 1 -type f -name '[0-9]*.sh' | sort -V)
  rc=0
  for f in "$@"; do
    run_scenario "$(basename "$f" .sh)" || rc=1
  done
  exit "$rc"
fi

preflight_lab
rc=0
for name in "$@"; do
  case "$name" in
    *[!0-9]*) ;;
    *)
      resolved="$(find "$SCENDIR" -maxdepth 1 -type f -name '[0-9]*.sh' | sort -V | awk -F/ -v wanted="$name" '{ base=$NF; split(base,p,"-"); if ((p[1]+0)==(wanted+0)) { print base; exit } }')"
      [ -n "$resolved" ] || { echo "unknown scenario number: $name"; rc=1; continue; }
      name="${resolved%.sh}"
      ;;
  esac
  case "$name" in
    *net*) # net = prefix match for groups
      for f in "$SCENDIR"/"$name"*.sh; do
        [ -e "$f" ] || { echo "no scenarios match $name"; rc=1; continue; }
        run_scenario "$(basename "$f" .sh)" || rc=1
      done
      ;;
    *)
      run_scenario "$name" || rc=1
      ;;
  esac
done
exit "$rc"
