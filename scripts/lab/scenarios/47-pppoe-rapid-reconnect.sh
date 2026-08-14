#!/bin/sh
# 47 — PPPoE rapid reconnect: five quick ISP-side session drops. Every cycle
# must re-establish cleanly with no zombie sessions and no state drift.
. "$(dirname "$0")/../lib.sh"
begin "47-pppoe-rapid-reconnect"
phase "3-fault"
require "fault: none (rapid reconnect)" ispfault status
phase "4-mr-runtime"
check "MR up before cycles" mr "uptime -s | grep -q ."
phase "4.5-operator"
for cycle in 1 2 3 4 5; do
  require "cycle $cycle: drop session" ispfault pppoe stop
  require "cycle $cycle: session goes down" wait_pppoe_down 45
  require "cycle $cycle: restart session" ispfault pppoe start
  require "cycle $cycle: session back up" wait_pppoe 90
  check "cycle $cycle: no zombie pppd" test "$(mr 'pgrep pppd | wc -l' 2>/dev/null | tr -d ' \n')" -le 3
done
phase "4-mr-runtime-2"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
