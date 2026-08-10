#!/bin/sh
# 48 — NTP time jump: a large clock step (forward then back) must not crash
# the router or break sessions; chrony recovers and the router stays usable.
. "$(dirname "$0")/../lib.sh"
begin "48-ntp-time-jump"
phase "3-fault"
require "fault: none (clock step)" ispfault status
phase "4-mr-runtime"
check "MR up before jump" mr "uptime -s | grep -q ."
phase "4.5-operator"
now=$(mr 'date +%s' | tr -d ' \n')
future=$((now + 172800))
past=$((now - 172800))
require "step clock forward 48h" mr "date -s @$future"
sleep 2
require "step clock back 48h" mr "date -s @$past"
sleep 2
phase "4-mr-runtime-2"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "router-applyd still alive" mr "rc-service router-applyd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
check "PPPoE session survived clock jump" check_pppoe
phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
