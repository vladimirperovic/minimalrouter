#!/bin/sh
# 108 — An authenticated session reports a non-empty CSRF token and write capability.
. "$(dirname "$0")/../lib.sh"
begin "108-session-csrf-contract"
phase "3-fault"
require "fault: none (session contract)" ispfault status
phase "4.5-operator"
api_login
resp="$(api GET /api/v1/auth/session)"
csrf="$(echo "$resp" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token", ""))' 2>/dev/null)"
readonly="$(echo "$resp" | python3 -c 'import json,sys; print(str(json.load(sys.stdin).get("read_only", True)).lower())' 2>/dev/null)"
check "CSRF token returned" test -n "$csrf"
check "administrator session writable" test "$readonly" = "false"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
