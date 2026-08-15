#!/bin/sh
# 121 — pfSense preview accepts XML only; JSON must be rejected before parsing.
. "$(dirname "$0")/../lib.sh"
begin "121-pfsense-content-type"
phase "3-fault"
require "fault: none (pfSense media type)" ispfault status
phase "4.5-operator"
api_login
code="$(api_status POST /api/v1/import/pfsense/preview '{}' application/json)"
check "JSON pfSense upload rejected" test "$code" = "415"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
