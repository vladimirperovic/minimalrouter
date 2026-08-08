#!/bin/sh
# 09 — packet loss 100% on the access link: full link blackout (frames are
# dropped, carrier stays up). PPPoE dies with it; local services must be
# unaffected and everything must return once loss is cleared.
. "$(dirname "$0")/../lib.sh"

begin "09-loss-100"
phase "3-fault"
require "fault: 100% loss both directions" ispfault loss 100
require "symptom: session drops under 100% loss" wait_pppoe_down 60

phase "4-mr-runtime"
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up" check_lan_up
check "local DNS still serves" check_local_dns
check "local save still works" mr_save_lease

phase "5-lan-client"
check "client lease intact" lan "ip -4 -o addr show | grep -q '192.168.1.'"

phase "6-revert"
require "fault: loss cleared" ispfault loss 0

phase "7-recovery"
require "PPPoE reconnects after full loss" wait_pppoe 150
check "LAN client internet back" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
