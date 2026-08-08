#!/bin/sh
# 07 — packet loss 5%: session survives; internet degraded but functional.
. "$(dirname "$0")/../lib.sh"

begin "07-loss-5"
phase "3-fault"
require "fault: 5% loss both directions" ispfault loss 5

phase "4-5-runtime"
check "PPPoE session survives 5% loss" check_pppoe
check "LAN client internet works (retried)" lan "curl -s --retry 3 --retry-delay 1 --max-time 15 http://$SIM_INET/marker.txt | grep -q torture-lab"
check "local DNS works" check_local_dns

phase "6-revert"
require "fault: loss cleared" ispfault loss 0

phase "7-recovery"
check "full quality restored" check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
