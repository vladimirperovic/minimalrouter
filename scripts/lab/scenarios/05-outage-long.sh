#!/bin/sh
# 05 — long ISP outage (10 min): everything local must run unattended; PPPoE
# must reconnect when the ISP returns without operator action.
. "$(dirname "$0")/../lib.sh"

begin "05-outage-long"
phase "3-fault"
require "fault: long outage started" ispfault outage long
require "symptom: session dropped" wait_pppoe_down 60

phase "4-mr-runtime"
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up" check_lan_up
check "local DNS still serves" check_local_dns
check "local save still works" mr_save_lease

# Exercise a real long-lived outage. Check local invariants at every minute so
# a transient failure cannot hide behind a healthy final state.
outage_seconds="${LAB_LONG_OUTAGE_SECONDS:-600}"
case "$outage_seconds" in
  *[!0-9]*|'') finish_scenario 1 ;;
esac
elapsed=0
while [ "$elapsed" -lt "$outage_seconds" ]; do
  remaining=$((outage_seconds-elapsed))
  interval=60
  [ "$remaining" -ge "$interval" ] || interval="$remaining"
  sleep "$interval"
  elapsed=$((elapsed+interval))
  check "firewall policy-drop at ${elapsed}s outage" check_fw_not_fail_open
  check "LAN up at ${elapsed}s outage" check_lan_up
  check "local DNS serves at ${elapsed}s outage" check_local_dns
  check "routerd/applyd healthy at ${elapsed}s outage" mr "rc-service routerd status | grep -q started && rc-service router-applyd status | grep -q started"
done

phase "5-lan-client"
check "client lease intact" lan "ip -4 -o addr show | grep -q '192.168.1.'"

phase "6-revert"
require "fault: long outage ended" ispfault outage stop

phase "7-recovery"
require "PPPoE reconnects after long outage" wait_pppoe 150
check "LAN client internet back" check_lan_internet
check "canonical + last-good converge" check_converge
check "runtime not hybrid" check_runtime_not_hybrid
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
