#!/bin/sh
# 16 — Internet blackhole: ISP keeps the PPPoE session but drops all forwarded
# traffic. Local services unaffected; clear the fault and internet returns.
. "$(dirname "$0")/../lib.sh"

begin "16-internet-blackhole"
phase "3-fault"
require "fault: forwarded traffic blackholed" ispfault blackhole on
sleep 3

phase "4-mr-runtime"
check "PPPoE session still up (not an outage)" check_pppoe
check "LAN still up" check_lan_up
check "local DNS still serves" check_local_dns
check "local save still works" mr_save_lease

phase "5-lan-client"
check "client has no internet while blackholed" lan "! curl -s --max-time 5 http://$SIM_INET/marker.txt >/dev/null 2>&1"

phase "6-revert"
require "fault: blackhole cleared" ispfault blackhole off

phase "7-recovery"
check "LAN client internet back" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
