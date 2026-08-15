#!/bin/sh
# 104 — Unsupported HTTP methods are rejected without invoking a mutation handler.
. "$(dirname "$0")/../lib.sh"
begin "104-api-method-boundary"
phase "3-fault"
require "fault: none (method boundary)" ispfault status
phase "4.5-operator"
api_login
code="$(api_status POST /api/v1/config '{}')"
method_rejected() { test "$1" = "404" || test "$1" = "405"; }
check "POST config rejected" method_rejected "$code"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
