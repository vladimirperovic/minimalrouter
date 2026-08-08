#!/bin/sh
# 01 — PPPoE server stop/start: session drops, LAN/DNS/local-save must survive,
# and MR-TEST must re-establish PPPoE automatically once the server returns.
. "$(dirname "$0")/../lib.sh"

begin "01-pppoe-stop-start"
phase "3-fault"
require "fault: pppoe server stopped" ispfault pppoe stop
require "symptom: PPPoE session dropped" wait_pppoe_down 60

phase "4-mr-runtime"
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up (host)" H ping -c1 -W2 192.168.1.1
check "LAN still up (client)" check_lan_up
check "local DNS still serves" check_local_dns
check "local save does not depend on PPPoE" mr_save_lease
check "routerd/applyd healthy" mr "rc-service routerd status | grep -q started && rc-service router-applyd status | grep -q started"

phase "5-lan-client"
check "client lease intact" lan "ip -4 -o addr show | grep -q '192.168.1.'"
check "client cannot reach internet (expected)" lan "! curl -s --max-time 4 http://$SIM_INET/marker.txt >/dev/null 2>&1"

phase "6-revert"
require "fault: pppoe server restarted" ispfault pppoe start

phase "7-recovery"
require "PPPoE auto-recovers" wait_pppoe 120
check "LAN client internet back" check_lan_internet
check "canonical + last-good converge" check_converge
check "runtime not hybrid" check_runtime_not_hybrid
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
