#!/bin/sh
# 112 — Gateway history rejects windows beyond the documented 30-day maximum.
. "$(dirname "$0")/../lib.sh"
begin "112-gateway-window-validation"
phase "3-fault"
require "fault: none (gateway window)" ispfault status
phase "4.5-operator"
api_login
code="$(api_status GET '/api/v1/gateway/history?window=90d')"
check "invalid history window rejected" test "$code" = "400"
check "valid history still available" test "$(api_status GET '/api/v1/gateway/history?window=1h')" = "200"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
