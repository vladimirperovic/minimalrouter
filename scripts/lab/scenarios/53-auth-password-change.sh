#!/bin/sh
# 53 — Password change: the new password works, the old one is rejected, and
# the original lab credential is restored even if a later assertion fails.
. "$(dirname "$0")/../lib.sh"
begin "53-auth-password-change"
phase "3-fault"
require "fault: none (password)" ispfault status

NEWPW="MinimalRouter-Lab-Rotated!2026"
PASSWORD_ROTATED=0
restore_password() {
  [ "$PASSWORD_ROTATED" -eq 1 ] || return 0
  resp="$(lan "curl -sk -m 10 -c /tmp/pw-restore-cookie.txt -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$NEWPW\"}'" 2>/dev/null || true)"
  token="$(echo "$resp" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null || true)"
  [ -n "$token" ] && lan "curl -sk -m 10 -b /tmp/pw-restore-cookie.txt -X POST $MR_API/api/v1/auth/change-password -H 'Content-Type: application/json' -H 'X-CSRF-Token: $token' -d '{\"old_password\":\"$NEWPW\",\"new_password\":\"$ADMIN_PW\"}'" >/dev/null 2>&1 || true
}
trap restore_password EXIT HUP INT TERM

phase "4.5-operator"
wait_login_window
login="$(lan "curl -sk -m 10 -c /tmp/pw-cookie.txt -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\"}'")"
csrf="$(echo "$login" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null)"
require "administrator session created" test -n "$csrf"
changed="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -b /tmp/pw-cookie.txt -X POST $MR_API/api/v1/auth/change-password -H 'Content-Type: application/json' -H 'X-CSRF-Token: $csrf' -d '{\"old_password\":\"$ADMIN_PW\",\"new_password\":\"$NEWPW\"}'")"
require "password change succeeds" test "$changed" = "200"
PASSWORD_ROTATED=1
old_status="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\"}'")"
check "old password rejected" test "$old_status" = "401"
new_login="$(lan "curl -sk -m 10 -c /tmp/pw-new-cookie.txt -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$NEWPW\"}'")"
csrf2="$(echo "$new_login" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null)"
require "new password accepted" test -n "$csrf2"

phase "4.5-cleanup"
restored="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -b /tmp/pw-new-cookie.txt -X POST $MR_API/api/v1/auth/change-password -H 'Content-Type: application/json' -H 'X-CSRF-Token: $csrf2' -d '{\"old_password\":\"$NEWPW\",\"new_password\":\"$ADMIN_PW\"}'")"
require "original password restored" test "$restored" = "200"
PASSWORD_ROTATED=0
lan "rm -f $API_COOKIE" >/dev/null 2>&1
rm -f "$API_CSRF"
require "original password works" api_login
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
