#!/bin/sh
# 29 — Enable the authenticated LAN proxy, fetch through it, then restore the
# exact original Squid configuration.
. "$(dirname "$0")/../lib.sh"
begin "29-squid"
phase "3-fault"
require "fault: none (config change)" ispfault status
api_login
original="$(api GET /api/v1/config)"
enabled="$(echo "$original" | python3 -c 'import json,sys; print(str(json.load(sys.stdin)["squid_proxy"]["enabled"]).lower())')"
require "scenario starts with Squid disabled" test "$enabled" = "false"
restore_squid() { api PUT /api/v1/config "$original" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true; }
trap restore_squid EXIT HUP INT TERM
phase "4.5-operator"
candidate="$(echo "$original" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["squid_proxy"].update({"enabled":True,"username":"labproxy","password":"lab-proxy-pass-2026","port":3128})
print(json.dumps(c))')"
require "enable Squid" api PUT /api/v1/config "$candidate"
require "Squid change confirmed" confirm_pending
require "Squid listening on 3128" retry 120 mr "ss -tlnp | grep -q ':3128'"
phase "5-lan-client"
check "client fetches through authenticated Squid" lan "curl -s --max-time 10 -x http://labproxy:lab-proxy-pass-2026@192.168.1.1:3128 http://$SIM_INET/marker.txt | grep -q torture-lab"
check "Squid logged the request" mr "grep -q 'TCP_MISS' /var/log/squid/access.log 2>/dev/null"
phase "4.5-revert"
require "restore Squid disabled" restore_squid
trap - EXIT HUP INT TERM
require "Squid stopped" retry 120 mr "! ss -tlnp | grep -q ':3128'"
check "internet still works after Squid restore" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
