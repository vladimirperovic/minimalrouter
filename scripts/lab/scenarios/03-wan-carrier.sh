#!/bin/sh
# 03 — WAN carrier down/up on the ISP access NIC. PPPoE dies with the link and
# must come back when the carrier returns, without operator action.
. "$(dirname "$0")/../lib.sh"

begin "03-wan-carrier"
phase "3-fault"
require "fault: ISP access NIC carrier down" ispfault carrier down
require "symptom: session dropped" wait_pppoe_down 60

phase "4-mr-runtime"
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up" check_lan_up
check "local DNS still serves" check_local_dns
check "local save still works" mr_save_lease

phase "5-lan-client"
check "client lease intact" lan "ip -4 -o addr show | grep -q '192.168.1.'"

phase "6-revert"
require "fault: ISP access NIC carrier up" ispfault carrier up

phase "7-recovery"
require "PPPoE auto-recovers after carrier up" wait_pppoe 150
check "LAN client internet back" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
