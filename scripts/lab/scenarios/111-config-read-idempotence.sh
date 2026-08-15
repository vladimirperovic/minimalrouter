#!/bin/sh
# 111 — Repeated configuration reads never advance the canonical revision.
. "$(dirname "$0")/../lib.sh"
begin "111-config-read-idempotence"
phase "3-fault"
require "fault: none (read idempotence)" ispfault status
phase "4.5-operator"
api_login
first="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
for i in 1 2 3 4 5 6 7 8 9 10; do api GET /api/v1/config >/dev/null 2>&1; done
last="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
check "GET leaves revision unchanged" test "$first" = "$last"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
