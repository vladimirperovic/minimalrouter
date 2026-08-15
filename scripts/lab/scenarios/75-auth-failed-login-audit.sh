#!/bin/sh
# 75 — A rejected credential is recorded as auth.login_failed with a source
# actor, without exposing the submitted password.
. "$(dirname "$0")/../lib.sh"
begin "75-auth-failed-login-audit"
phase "3-fault"
require "fault: none (audit)" ispfault status
phase "4.5-operator"
code="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"wrong-password\"}'")"
require "invalid credential rejected" test "$code" = "401"
sleep 2
api_login
events="$(api GET '/api/v1/audit/events?limit=100')"
found="$(echo "$events" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(any(e.get("event_type")=="auth.login_failed" and e.get("actor") for e in d.get("events",[])))' 2>/dev/null)"
check "failed login recorded with actor" test "$found" = "True"
check "submitted password absent from audit payload" test "$(printf '%s' "$events" | grep -Fc wrong-password)" -eq 0
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
