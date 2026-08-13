#!/bin/sh
# 74 — Malformed JSON receives 400 exactly and cannot advance revision.
. "$(dirname "$0")/../lib.sh"
begin "74-api-malformed-body"
phase "3-fault"
require "fault: none (malformed)" ispfault status
phase "4.5-operator"
api_login
before="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
code="$(api_status PUT /api/v1/config 'not-json{{{')"
check "malformed body rejected with 400" test "$code" = "400"
after="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
check "malformed body leaves revision unchanged" test "$before" = "$after"
check "valid request still works" api GET /api/v1/config
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
