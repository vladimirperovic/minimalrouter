#!/bin/sh
# 73 — A password change revokes every existing session, not only the cookie
# that submitted the change; the original lab password is restored safely.
. "$(dirname "$0")/../lib.sh"
begin "73-api-session-invalidation"
phase "3-fault"
require "fault: none (session invalidation)" ispfault status

NEWPW="MinimalRouter-Lab-Rotated!2026"
PASSWORD_ROTATED=0
restore_password() {
  [ "$PASSWORD_ROTATED" -eq 1 ] || return 0
  login="$(lan "curl -sk -m 10 -c /tmp/inv-clean-cookie.txt -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$NEWPW\"}'" 2>/dev/null || true)"
  token="$(echo "$login" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null || true)"
  [ -n "$token" ] && lan "curl -sk -m 10 -b /tmp/inv-clean-cookie.txt -X POST $MR_API/api/v1/auth/change-password -H 'Content-Type: application/json' -H 'X-CSRF-Token: $token' -d '{\"old_password\":\"$NEWPW\",\"new_password\":\"$ADMIN_PW\"}'" >/dev/null 2>&1 || true
}
trap restore_password EXIT HUP INT TERM

phase "4.5-operator"
wait_login_window
login_a="$(lan "curl -sk -m 10 -c /tmp/inv-a-cookie.txt -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\"}'")"
csrf_a="$(echo "$login_a" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null)"
login_b="$(lan "curl -sk -m 10 -c /tmp/inv-b-cookie.txt -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\"}'")"
require "two independent sessions created" test -n "$csrf_a" -a -n "$login_b"
before="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -b /tmp/inv-b-cookie.txt $MR_API/api/v1/config")"
check "second session works before change" test "$before" = "200"
changed="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -b /tmp/inv-a-cookie.txt -X POST $MR_API/api/v1/auth/change-password -H 'Content-Type: application/json' -H 'X-CSRF-Token: $csrf_a' -d '{\"old_password\":\"$ADMIN_PW\",\"new_password\":\"$NEWPW\"}'")"
require "password change succeeds" test "$changed" = "200"
PASSWORD_ROTATED=1
stale="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -b /tmp/inv-b-cookie.txt $MR_API/api/v1/config")"
check "other pre-existing session revoked" test "$stale" = "401"

phase "4.5-cleanup"
new_login="$(lan "curl -sk -m 10 -c /tmp/inv-new-cookie.txt -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$NEWPW\"}'")"
csrf_new="$(echo "$new_login" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null)"
require "new credential creates replacement session" test -n "$csrf_new"
restored="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -b /tmp/inv-new-cookie.txt -X POST $MR_API/api/v1/auth/change-password -H 'Content-Type: application/json' -H 'X-CSRF-Token: $csrf_new' -d '{\"old_password\":\"$NEWPW\",\"new_password\":\"$ADMIN_PW\"}'")"
require "original password restored" test "$restored" = "200"
PASSWORD_ROTATED=0
lan "rm -f $API_COOKIE" >/dev/null 2>&1
rm -f "$API_CSRF"
check "original password works" api_login
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
