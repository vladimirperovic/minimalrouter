#!/bin/sh
# 45 — Failed apply rollback: submitting a config that validation rejects
# (bogus LAN address) must be refused cleanly; canonical state and last-good
# stay intact and convergent, and the router keeps serving.
. "$(dirname "$0")/../lib.sh"
begin "45-config-rollback-failed-apply"
phase "3-fault"
require "fault: none (config injection)" ispfault status
phase "4-mr-runtime"
check "MR up" mr "uptime -s | grep -q ."
check "converged before" check_converge
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)" || finish_scenario 1
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["lan"]["ip_address"]="999.999.999.999"
print(json.dumps(c))')"
check "invalid config rejected" sh -c "! api PUT /api/v1/config \"$bad\" 2>/dev/null | grep -q '\"success\": true'"
phase "4-mr-runtime-2"
check "canonical unchanged" api_login && api GET /api/v1/config | python3 -c 'import json,sys; c=json.load(sys.stdin); assert c["lan"]["ip_address"]=="192.168.1.1"'
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
