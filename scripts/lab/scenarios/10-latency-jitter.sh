#!/bin/sh
# 10 — latency + jitter: 80ms RTT with 25ms jitter, 0.5% loss. Session and
# services must keep working, just slower.
. "$(dirname "$0")/../lib.sh"

begin "10-latency-jitter"
phase "3-fault"
require "fault: 80ms + 25ms jitter + 0.5% loss" ispfault latency 80 25 0.5

phase "4-5-runtime"
check "PPPoE session survives" check_pppoe
check "LAN client internet through latency" lan "curl -s --max-time 20 http://$SIM_INET/marker.txt | grep -q torture-lab"
check "local DNS fast and working" check_local_dns

phase "6-revert"
require "fault: cleared" ispfault latency 0 0 0

phase "7-recovery"
check "latency qdiscs removed" isp "! tc qdisc show dev $(cat /etc/lab-iface 2>/dev/null || echo ens18) 2>/dev/null | grep -q netem"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
