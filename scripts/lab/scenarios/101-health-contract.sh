#!/bin/sh
# 101 — Health API returns a structured appliance snapshot with individual checks.
. "$(dirname "$0")/../lib.sh"
begin "101-health-contract"
phase "3-fault"
require "fault: none (health contract)" ispfault status
phase "4.5-operator"
api_login
resp="$(api GET /api/v1/health)"
state="$(echo "$resp" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("state", ""))' 2>/dev/null)"
checks="$(echo "$resp" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(len(d.get("checks") or []))' 2>/dev/null)"
check "health state present" test -n "$state"
check "health contains checks" test "${checks:-0}" -gt 0
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
