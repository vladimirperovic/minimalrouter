#!/bin/sh
# 105 — HTTPS responses include HSTS and MIME-sniffing protection headers.
. "$(dirname "$0")/../lib.sh"
begin "105-security-headers-hsts"
phase "3-fault"
require "fault: none (security headers)" ispfault status
phase "4.5-operator"
headers="$(lan "curl -skD - -o /dev/null --max-time 10 $MR_API/api/v1/setup/status")"
check "HSTS present" test "$(printf '%s' "$headers" | grep -ci '^strict-transport-security:')" -gt 0
check "nosniff present" test "$(printf '%s' "$headers" | grep -ci '^x-content-type-options: *nosniff')" -gt 0
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
