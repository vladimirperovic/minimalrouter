#!/bin/sh
# 107 — Login session cookies are Secure, HttpOnly and SameSite restricted.
. "$(dirname "$0")/../lib.sh"
begin "107-session-cookie-flags"
phase "3-fault"
require "fault: none (cookie flags)" ispfault status
phase "4.5-operator"
headers="$(lan "curl -ski --max-time 10 -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\"}'")"
cookie="$(printf '%s' "$headers" | grep -i '^set-cookie:' | head -1)"
check "session cookie emitted" test -n "$cookie"
check "cookie Secure" test "$(printf '%s' "$cookie" | grep -ci 'secure')" -gt 0
check "cookie HttpOnly" test "$(printf '%s' "$cookie" | grep -ci 'httponly')" -gt 0
check "cookie SameSite" test "$(printf '%s' "$cookie" | grep -ci 'samesite')" -gt 0
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
