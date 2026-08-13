#!/bin/sh
# 56 — Rapid login churn activates rate limiting without wedging routerd; a
# valid login works again after the limiter window expires.
. "$(dirname "$0")/../lib.sh"
begin "56-api-rate-limit"
phase "3-fault"
require "fault: none (api rate)" ispfault status
phase "4.5-operator"
wait_login_window
limited=0
for i in $(seq 1 40); do
  code="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\"}'")"
  [ "$code" = "429" ] && limited=$((limited+1))
done
check "login limiter activated during churn" test "$limited" -gt 0
check "routerd responsive after churn" retry 60 mr "rc-service routerd status | grep -q started"
wait_login_window
lan "rm -f $API_COOKIE" >/dev/null 2>&1
rm -f "$API_CSRF"
check "login still works after churn" api_login
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
