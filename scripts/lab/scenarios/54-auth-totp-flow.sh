#!/bin/sh
# 54 — TOTP lifecycle: enrollment requires the administrator password,
# enabling requires a valid code, login requires TOTP, and disable restores
# password-only authentication.
. "$(dirname "$0")/../lib.sh"
begin "54-auth-totp-flow"
phase "3-fault"
require "fault: none (totp)" ispfault status

TOTP_ENABLED=0
secret=""
cleanup_totp() {
  [ "$TOTP_ENABLED" -eq 1 ] && [ -n "$secret" ] || return 0
  wait_next_totp
  login_code="$(totp_code "$secret")"
  login="$(lan "curl -sk -m 10 -c /tmp/totp-clean-cookie.txt -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\",\"totp_code\":\"$login_code\"}'" 2>/dev/null || true)"
  token="$(echo "$login" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null || true)"
  [ -n "$token" ] || return 0
  wait_next_totp
  disable_code="$(totp_code "$secret")"
  lan "curl -sk -m 10 -b /tmp/totp-clean-cookie.txt -X POST $MR_API/api/v1/auth/totp/disable -H 'Content-Type: application/json' -H 'X-CSRF-Token: $token' -d '{\"code\":\"$disable_code\",\"current_password\":\"$ADMIN_PW\"}'" >/dev/null 2>&1 || true
}
trap cleanup_totp EXIT HUP INT TERM

phase "4.5-operator"
api_login
resp="$(api POST /api/v1/auth/totp/enroll "{\"current_password\":\"$ADMIN_PW\"}")"
secret="$(echo "$resp" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("secret",""))' 2>/dev/null)"
require "enrollment returns a base32 secret" test -n "$secret"
enable_code="$(totp_code "$secret")"
require "valid TOTP enables two-factor auth" api POST /api/v1/auth/totp/enable "{\"code\":\"$enable_code\"}"
TOTP_ENABLED=1

wait_login_window
without_code="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\"}'")"
check "password-only login rejected while TOTP enabled" test "$without_code" = "401"
wait_next_totp
login_code="$(totp_code "$secret")"
login="$(lan "curl -sk -m 10 -c /tmp/totp-cookie.txt -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\",\"totp_code\":\"$login_code\"}'")"
csrf="$(echo "$login" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null)"
check "login with fresh TOTP succeeds" test -n "$csrf"

phase "4.5-cleanup"
wait_next_totp
disable_code="$(totp_code "$secret")"
disabled="$(lan "curl -sk -m 10 -o /dev/null -w '%{http_code}' -b /tmp/totp-cookie.txt -X POST $MR_API/api/v1/auth/totp/disable -H 'Content-Type: application/json' -H 'X-CSRF-Token: $csrf' -d '{\"code\":\"$disable_code\",\"current_password\":\"$ADMIN_PW\"}'")"
require "valid TOTP disables two-factor auth" test "$disabled" = "200"
TOTP_ENABLED=0
lan "rm -f $API_COOKIE" >/dev/null 2>&1
rm -f "$API_CSRF"
check "password-only login works after disable" api_login
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
