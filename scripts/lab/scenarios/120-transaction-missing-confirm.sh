#!/bin/sh
# 120 — Confirming an unknown transaction cannot change canonical state.
. "$(dirname "$0")/../lib.sh"
begin "120-transaction-missing-confirm"
phase "3-fault"
require "fault: none (missing transaction)" ispfault status
phase "4.5-operator"
api_login
before="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
code="$(api_status POST /api/v1/transactions/lab-does-not-exist/confirm '{}')"
after="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
check "unknown transaction rejected" test "$code" = "409"
check "revision unchanged" test "$before" = "$after"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
