#!/bin/sh
# 106 — Management responses carry CSP and frame-embedding protection.
. "$(dirname "$0")/../lib.sh"
begin "106-security-headers-csp"
phase "3-fault"
require "fault: none (csp headers)" ispfault status
phase "4.5-operator"
headers="$(lan "curl -skD - -o /dev/null --max-time 10 $MR_API/api/v1/setup/status")"
check "CSP present" test "$(printf '%s' "$headers" | grep -ci '^content-security-policy:')" -gt 0
check "frame protection present" test "$(printf '%s' "$headers" | grep -ciE '^x-frame-options:|frame-ancestors')" -gt 0
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
