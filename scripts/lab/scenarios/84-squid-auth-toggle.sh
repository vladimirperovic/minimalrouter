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
note "preserving original Squid enabled state: $original_enabled"
restore_squid() {
	# PUT requires the current revision. Reuse the live envelope while restoring
	# only the Squid object, otherwise the old `original` revision is rejected as
	# stale after the enable transaction and cleanup silently leaves Squid on.
	current="$(api GET /api/v1/config 2>/dev/null)" || return 1
	restore="$(printf '%s' "$current" | ORIGINAL_JSON="$original" python3 -c '
import json,os,sys
current=json.load(sys.stdin)
original=json.loads(os.environ["ORIGINAL_JSON"])
current["squid_proxy"] = original["squid_proxy"]
print(json.dumps(current))
')" || return 1
	api PUT /api/v1/config "$restore" >/dev/null 2>&1 || return 1
	confirm_pending >/dev/null 2>&1
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
check "valid proxy credentials fetch marker" retry 60 lan "curl -s --max-time 10 -x http://labproxy:lab-proxy-pass-2026@192.168.1.1:3128 http://$SIM_INET/marker.txt | grep -q torture-lab"
check "invalid proxy credentials are denied" retry 60 lan "code=\$(curl -s --max-time 10 -o /dev/null -w '%{http_code}' -x http://labproxy:wrong-password@192.168.1.1:3128 http://$SIM_INET/marker.txt); [ \"\$code\" = 407 ]"

phase "4.5-cleanup"
restore_squid
trap - EXIT HUP INT TERM
if [ "$original_enabled" = "true" ]; then
  require "Squid remains available after restoring enabled baseline" retry 60 mr "ss -tln | grep -q ':3128'"
else
  require "Squid stops after restoring disabled baseline" retry 60 mr "! ss -tln | grep -q ':3128'"
fi
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
