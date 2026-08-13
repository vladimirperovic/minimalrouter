#!/bin/sh
# 84 — Squid authentication is enabled, exercised by a LAN client, and the
# exact original configuration is restored on every exit path.
. "$(dirname "$0")/../lib.sh"
begin "84-squid-auth-toggle"
phase "3-fault"
require "fault: none (squid)" ispfault status
phase "4.5-operator"
api_login
original="$(api GET /api/v1/config)"
original_enabled="$(echo "$original" | python3 -c 'import json,sys; print(str(json.load(sys.stdin)["squid_proxy"]["enabled"]).lower())')"
check "scenario starts with Squid disabled" test "$original_enabled" = "false"
note "proceeding even if Squid was already enabled (canonical leakage)"
restore_squid() {
  api PUT /api/v1/config "$original" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap restore_squid EXIT HUP INT TERM

new="$(echo "$original" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["squid_proxy"].update({"enabled":True,"port":3128,"username":"labproxy","password":"lab-proxy-pass-2026"})
print(json.dumps(c))')"
require "authenticated Squid configuration saved" api PUT /api/v1/config "$new"
require "Squid change confirmed" confirm_pending
require "Squid listens on LAN" retry 60 mr "ss -tln | grep -q ':3128'"
check "valid proxy credentials fetch marker" lan "curl -s --max-time 10 -x http://labproxy:lab-proxy-pass-2026@192.168.1.1:3128 http://$SIM_INET/marker.txt | grep -q torture-lab"
check "invalid proxy credentials are denied" lan "code=\$(curl -s --max-time 10 -o /dev/null -w '%{http_code}' -x http://labproxy:wrong-password@192.168.1.1:3128 http://$SIM_INET/marker.txt); [ \"\$code\" = 407 ]"

phase "4.5-cleanup"
restore_squid
trap - EXIT HUP INT TERM
if [ "$original_enabled" = "false" ]; then
  require "Squid stops after restore" retry 60 mr "! ss -tln | grep -q ':3128'"
else
  note "Squid was already enabled at start; skip stop assertion"
fi
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
