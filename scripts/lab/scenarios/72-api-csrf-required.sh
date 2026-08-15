#!/bin/sh
# 72 — A valid session cookie without its CSRF token receives 403 on mutation,
# while the same session remains usable for reads.
. "$(dirname "$0")/../lib.sh"
begin "72-api-csrf-required"
phase "3-fault"
require "fault: none (csrf)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
cfg_b64="$(printf '%s' "$cfg" | base64 | tr -d '\n')"
code="$(lan "echo '$cfg_b64' | base64 -d | curl -sk -m 10 -o /dev/null -w '%{http_code}' -b $API_COOKIE -X PUT -H 'Content-Type: application/json' --data-binary @- $MR_API/api/v1/config")"
require "mutation without CSRF rejected" test "$code" = "403"
check "read still works" api GET /api/v1/config
check "revision remains unchanged" test "$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')" = "$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
