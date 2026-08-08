#!/bin/sh
# 05 — long ISP outage (10 min): everything local must run unattended; PPPoE
# must reconnect when the ISP returns without operator action.
. "$(dirname "$0")/../lib.sh"

begin "05-outage-long"
phase "3-fault"
require "fault: long outage started" ispfault outage long
require "symptom: session dropped" wait_pppoe_down 60

phase "4-mr-runtime"
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up" check_lan_up
check "local DNS still serves" check_local_dns
check "local save still works" mr_save_lease
check "routerd/applyd healthy after 60s outage" mr "rc-service routerd status | grep -q started && rc-service router-applyd status | grep -q started"

phase "5-lan-client"
check "client lease intact" lan "ip -4 -o addr show | grep -q '192.168.1.'"

phase "6-revert"
require "fault: long outage ended" ispfault outage stop

phase "7-recovery"
require "PPPoE reconnects after long outage" wait_pppoe 150
check "LAN client internet back" check_lan_internet
check "canonical + last-good converge" check_converge
check "runtime not hybrid" check_runtime_not_hybrid
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
