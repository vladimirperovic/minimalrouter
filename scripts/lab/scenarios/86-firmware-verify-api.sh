#!/bin/sh
# 86 — The verification API parses a syntactically valid manifest but never
# accepts an unsigned image. A missing lab trust anchor is reported distinctly.
. "$(dirname "$0")/../lib.sh"
begin "86-firmware-verify-api"
phase "3-fault"
require "fault: none (firmware verify)" ispfault status
phase "4.5-operator"
api_login
manifest='{"version":"0.0.0-lab","build_date":"2026-08-11T00:00:00Z","git_commit":"lab","files":{},"signature":""}'
manifest_b64="$(printf '%s' "$manifest" | base64 | tr -d '\n')"
code="$(api_status POST /api/v1/firmware/verify "{\"manifest_b64\":\"$manifest_b64\"}")"
check "unsigned firmware is never accepted" sh -c "[ '$code' = 422 ] || [ '$code' = 503 ]"
if [ "$code" = "503" ]; then
  note "firmware trust anchor is intentionally absent in this lab image"
fi
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
