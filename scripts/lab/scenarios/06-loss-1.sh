#!/bin/sh
# 06 — packet loss 1% on the access link: negligible impact expected.
. "$(dirname "$0")/../lib.sh"

begin "06-loss-1"
phase "3-fault"
require "fault: 1% loss both directions" ispfault loss 1

phase "4-5-runtime"
check "PPPoE session survives 1% loss" check_pppoe
check "LAN client internet works through 1% loss" check_lan_internet
check "local DNS works" check_local_dns

phase "6-revert"
require "fault: loss cleared" ispfault loss 0

phase "7-recovery"
check "full quality restored" check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
