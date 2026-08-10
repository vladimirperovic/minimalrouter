#!/bin/sh
# 49 — Concurrent config saves: parallel PUTs must serialize cleanly (no
# interleaved state, no corruption, no lockup); final state converges.
. "$(dirname "$0")/../lib.sh"
begin "49-concurrent-config-saves"
phase "3-fault"
require "fault: none (concurrent saves)" ispfault status
phase "4-mr-runtime"
check "MR up before saves" mr "uptime -s | grep -q ."
check "converged before" check_converge
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)" || finish_scenario 1
for i in 1 2 3 4 5; do
  ( api_login
    c="$(echo "$cfg" | python3 -c "
import json,sys
c=json.load(sys.stdin)
c['dhcp']['lease_time']='$((i))h' if i>0 else '1h'
print(json.dumps(c))")"
    api PUT /api/v1/config "$c" >/dev/null 2>&1
    confirm_pending >/dev/null 2>&1 ) &
done
wait
sleep 5
phase "4-mr-runtime-2"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
phase "7-recovery"
check "canonical + last-good converge" retry 60 check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
