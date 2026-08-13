#!/bin/sh
# 49 — Five clients submit different candidates based on the same revision.
# Exactly one may commit; stale contenders must be rejected without corruption.
. "$(dirname "$0")/../lib.sh"
begin "49-concurrent-config-saves"
phase "3-fault"
require "fault: none (concurrent saves)" ispfault status
phase "4-mr-runtime"
require "converged before saves" check_converge
phase "4.5-operator"
api_login
original="$(api GET /api/v1/config)"
original_lease="$(echo "$original" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
restore_config() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  restored="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$original_lease'; print(json.dumps(c))")"
  api PUT /api/v1/config "$restored" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap restore_config EXIT HUP INT TERM

rm -f /tmp/lab49-status.*
for i in 1 2 3 4 5; do
  candidate="$(echo "$original" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='${i}h'; print(json.dumps(c))")"
  (api_status PUT /api/v1/config "$candidate" > "/tmp/lab49-status.$i") &
done
wait
accepted="$(awk '$0==200 || $0==202 {n++} END {print n+0}' /tmp/lab49-status.*)"
rejected="$(awk '$0==422 {n++} END {print n+0}' /tmp/lab49-status.*)"
check "exactly one concurrent save accepted" test "$accepted" -eq 1
check "four stale or pending saves rejected" test "$rejected" -eq 4
require "accepted transaction confirmed" confirm_pending
phase "4-mr-runtime-2"
check "canonical JSON remains readable" api GET /api/v1/config
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
phase "7-recovery"
restore_config
trap - EXIT HUP INT TERM
check "original lease setting restored" test "$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')" = "$original_lease"
check "canonical + last-good converge" retry 60 check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
