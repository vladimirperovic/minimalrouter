#!/bin/sh
# 109 — Audit pagination rejects limits outside the documented 1–500 range.
. "$(dirname "$0")/../lib.sh"
begin "109-audit-limit-validation"
phase "3-fault"
require "fault: none (audit limit)" ispfault status
phase "4.5-operator"
api_login
low="$(api_status GET '/api/v1/audit/events?limit=0')"
high="$(api_status GET '/api/v1/audit/events?limit=501')"
check "zero limit rejected" test "$low" = "400"
check "oversized limit rejected" test "$high" = "400"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
