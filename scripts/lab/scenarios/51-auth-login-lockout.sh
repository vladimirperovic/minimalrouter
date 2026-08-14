#!/bin/sh
# 51 — Login rate limiting: five failed logins consume the source quota; the
# sixth request is 429 and a valid login works after the window resets.
. "$(dirname "$0")/../lib.sh"
begin "51-auth-login-lockout"
phase "3-fault"
require "fault: none (auth)" ispfault status
phase "4.5-operator"
wait_login_window
rejected=0
for i in 1 2 3 4 5; do
  code="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"wrong-password\"}'")"
  [ "$code" = "401" ] && rejected=$((rejected+1))
done
check "first five invalid logins rejected as credentials" test "$rejected" -eq 5
sixth="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\"}'")"
require "sixth login rejected by rate limit" test "$sixth" = "429"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
wait_login_window
lan "rm -f $API_COOKIE" >/dev/null 2>&1
rm -f "$API_CSRF"
require "login works again after window" api_login
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
