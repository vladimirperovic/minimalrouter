#!/bin/sh
# 55 — Read-only sessions can read config but receive 403 on mutations.
. "$(dirname "$0")/../lib.sh"
begin "55-auth-readonly-session"
phase "3-fault"
require "fault: none (ro-session)" ispfault status
phase "4.5-operator"
wait_login_window
lan "rm -f /tmp/ro-cookie.txt" >/dev/null 2>&1
login="$(lan "curl -sk -m 10 -c /tmp/ro-cookie.txt -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\",\"read_only\":true}'")"
csrf="$(echo "$login" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null)"
require "read-only session issued" test -n "$csrf"
cfg="$(lan "curl -sk --fail-with-body -m 10 -b /tmp/ro-cookie.txt $MR_API/api/v1/config")"
check "read-only session can read" test "$(echo "$cfg" | grep -c '"revision"')" -gt 0
cfg_b64="$(printf '%s' "$cfg" | base64 | tr -d '\n')"
code="$(lan "echo '$cfg_b64' | base64 -d | curl -sk -m 10 -o /dev/null -w '%{http_code}' -b /tmp/ro-cookie.txt -X PUT -H 'Content-Type: application/json' -H 'X-CSRF-Token: $csrf' --data-binary @- $MR_API/api/v1/config")"
require "read-only session rejects PUT" test "$code" = "403"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
