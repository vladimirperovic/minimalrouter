#!/bin/sh
# 08 — packet loss 20%: heavy loss; PPPoE control frames (LCP echoes) must
# still survive; local services unaffected.
. "$(dirname "$0")/../lib.sh"

begin "08-loss-20"
phase "3-fault"
require "fault: 20% loss both directions" ispfault loss 20

phase "4-5-runtime"
check "PPPoE session survives 20% loss" check_pppoe
check "LAN still up" check_lan_up
check "local DNS still serves" check_local_dns
check "local save still works" mr_save_lease

phase "6-revert"
require "fault: loss cleared" ispfault loss 0

phase "7-recovery"
require "session stable after clear" wait_pppoe 60
check "LAN client internet back" check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
