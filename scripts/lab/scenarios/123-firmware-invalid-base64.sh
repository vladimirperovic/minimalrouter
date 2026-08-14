#!/bin/sh
# 123 — Firmware verification rejects a manifest that is not valid base64.
. "$(dirname "$0")/../lib.sh"
begin "123-firmware-invalid-base64"
phase "3-fault"
require "fault: none (firmware base64)" ispfault status
phase "4.5-operator"
api_login
code="$(api_status POST /api/v1/firmware/verify '{"manifest_b64":"%%%"}')"
check "malformed manifest rejected" test "$code" = "400"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
