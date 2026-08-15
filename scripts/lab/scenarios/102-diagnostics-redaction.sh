#!/bin/sh
# 102 — Diagnostic export is valid JSON and does not disclose configured secrets.
. "$(dirname "$0")/../lib.sh"
begin "102-diagnostics-redaction"
phase "3-fault"
require "fault: none (diagnostics)" ispfault status
phase "4.5-operator"
api_login
resp="$(api GET /api/v1/system/diagnostics)"
check "diagnostics are JSON" sh -c "printf '%s' \"\$1\" | python3 -m json.tool >/dev/null" sh "$resp"
check "admin password absent" test "$(printf '%s' "$resp" | grep -Fc "$ADMIN_PW")" -eq 0
check "redaction marker present" test "$(printf '%s' "$resp" | grep -Fc '[REDACTED]')" -gt 0
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
