#!/bin/sh
# 12 — bandwidth limiting (4 Mbps): throughput constrained, connection intact.
. "$(dirname "$0")/../lib.sh"

begin "12-rate-limit"
phase "3-fault"
require "fault: link rate limited to 4mbit" ispfault rate 4

phase "4-5-runtime"
check "PPPoE session survives" check_pppoe
check "LAN client internet works at limited rate" lan "curl -s --max-time 30 http://$SIM_INET/marker.txt | grep -q torture-lab"
check "throughput actually constrained" lan "curl -s --max-time 30 -o /dev/null -w '%{speed_download}' http://$SIM_INET/index.html | python3 -c 'import sys; v=float(sys.stdin.read()); sys.exit(0 if 0 < v < 700000 else 1)'"

phase "6-revert"
require "fault: cleared" ispfault rate 0

phase "7-recovery"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
