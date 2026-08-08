#!/bin/sh
# 11 — packet reorder 25%: out-of-order delivery on the access link.
. "$(dirname "$0")/../lib.sh"

begin "11-reorder"
phase "3-fault"
require "fault: 25% reorder" ispfault reorder 25

phase "4-5-runtime"
check "PPPoE session survives reorder" check_pppoe
check "LAN client internet works" check_lan_internet
check "local DNS works" check_local_dns

phase "6-revert"
require "fault: cleared" ispfault reorder 0

phase "7-recovery"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
