#!/bin/sh
# 103 — Canonical configuration is never readable without an authenticated session.
. "$(dirname "$0")/../lib.sh"
begin "103-unauthenticated-config-denied"
phase "3-fault"
require "fault: none (unauthenticated config)" ispfault status
phase "4.5-operator"
code="$(api_unauth_status GET /api/v1/config)"
check "unauthenticated config denied" test "$code" = "401"
api_login
check "authenticated config remains readable" test "$(api_status GET /api/v1/config)" = "200"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
