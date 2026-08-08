#!/bin/sh
# 02 — PPPoE authentication failure: wrong secret on the ISP side. Client must
# NOT get a session; everything local must keep working; restoring the secret
# + reconnect must recover.
. "$(dirname "$0")/../lib.sh"

begin "02-pppoe-auth-failure"
phase "3-fault"
require "fault: server secret set to wrong password" ispfault auth bad

phase "4-mr-runtime"
require "symptom: pppd cannot authenticate" ispfault pppoe stop
# force the client to reconnect against the bad secret
mr "rc-service pppoe-wan restart >/dev/null 2>&1" >/dev/null
sleep 12
require "fault: server back with bad secret" ispfault pppoe start
require "symptom: no session while secret is wrong" wait_pppoe_down 60
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up" check_lan_up
check "local DNS still serves" check_local_dns
check "local save still works" mr_save_lease

phase "6-revert"
require "fault: correct secret restored" ispfault auth good
require "fault: reconnect forced" ispfault pppoe stop

phase "7-recovery"
require "fault: server back up" ispfault pppoe start
require "PPPoE re-authenticates with good secret" wait_pppoe 120
check "LAN client internet back" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
