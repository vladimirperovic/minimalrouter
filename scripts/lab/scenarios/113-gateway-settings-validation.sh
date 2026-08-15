#!/bin/sh
# 113 — Gateway monitor rejects an empty target set and a zero sampling interval.
. "$(dirname "$0")/../lib.sh"
begin "113-gateway-settings-validation"
phase "3-fault"
require "fault: none (gateway settings)" ispfault status
phase "4.5-operator"
api_login
code="$(api_status PUT /api/v1/gateway/settings '{"enabled":true,"targets":[],"interval_seconds":0}')"
check "invalid gateway settings rejected" test "$code" = "400"
check "existing settings remain readable" test "$(api_status GET /api/v1/gateway/settings)" = "200"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
