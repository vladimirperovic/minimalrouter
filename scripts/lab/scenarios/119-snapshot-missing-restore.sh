#!/bin/sh
# 119 — Restoring an unknown snapshot returns 404 and leaves runtime untouched.
. "$(dirname "$0")/../lib.sh"
begin "119-snapshot-missing-restore"
phase "3-fault"
require "fault: none (missing snapshot)" ispfault status
phase "4.5-operator"
api_login
code="$(api_status POST /api/v1/snapshots/lab-does-not-exist/restore '{}')"
check "missing snapshot rejected" test "$code" = "404"
check "firewall remains fail-closed" check_fw_not_fail_open
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
