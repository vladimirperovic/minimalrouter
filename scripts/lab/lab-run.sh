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
. "$LABDIR/lib.sh"

log() { echo "[$(date +%H:%M:%S)] $*"; }

reset_lab() {
  log "=== reset lab to known state ==="
  ispfault reset >/dev/null 2>&1 || true
  disarm_hooks >/dev/null 2>&1 || true
  # DDNS verification must be deterministic: point the fake providers at the
  # sim (self-signed TLS fails fast) instead of the real no-ip/ipify.
  mr "grep -q '10.250.0.10 no-ip.com' /etc/hosts || echo '10.250.0.10 no-ip.com dynupdate.no-ip.com api.ipify.org' >> /etc/hosts; rc-service dnsmasq reload >/dev/null 2>&1 || true" >/dev/null 2>&1 || true
  # make sure PPPoE is negotiated before invariants
  if ! wait_pppoe 60; then
    log "PPPoE not up after reset; bouncing pppoe-wan on MR"
    mr 'rc-service pppoe-wan restart 2>/dev/null || true' >/dev/null 2>&1
    wait_pppoe 60 || true
  fi
  # production baseline snapshot (bridge ports must never change)
  PROD_PORTS_BEFORE="$(prod_ports_md5)"
  export PROD_PORTS_BEFORE
  log "production vmbr0 port fingerprint: $PROD_PORTS_BEFORE"
}

initial_invariants() {
  log "=== initial invariants ==="
  check_fw_not_fail_open && log "  firewall policy drop: OK" || log "  firewall policy drop: FAIL (continuing)"
  check_pppoe && log "  PPPoE 10.250.0.50: OK" || log "  PPPoE: FAIL"
  check_lan_up && log "  LAN reachable: OK" || log "  LAN: FAIL"
  check_local_dns && log "  local DNS: OK" || log "  local DNS: FAIL"
  check_lan_internet && log "  LAN->internet: OK" || log "  LAN->internet: FAIL"
}

run_scenario() {
  name="$1"
  file="$SCENDIR/$name.sh"
  if [ ! -f "$file" ]; then
    # allow bare numbers: 26 -> 26-enospc.sh
    m="$(ls "$SCENDIR"/"$name"-*.sh 2>/dev/null | head -1 || true)"
    [ -n "$m" ] && [ -f "$m" ] && file="$m"
  fi
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
    capture_state "postmortem"
    return 1
  fi
  log "scenario $name PASS"
  return 0
}

case "${1:-}" in
  list)
    echo "Scenarios:"
    for f in "$SCENDIR"/*.sh; do
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
  set -- "$SCENDIR"/*.sh
  for f in "$@"; do
    run_scenario "$(basename "$f" .sh)" || true
  done
  exit 0
fi

rc=0
for name in "$@"; do
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
