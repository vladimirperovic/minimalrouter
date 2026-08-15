#!/bin/sh
# 122 — Backup import rejects a request that is not a multipart backup upload.
. "$(dirname "$0")/../lib.sh"
begin "122-backup-missing-upload"
phase "3-fault"
require "fault: none (backup upload)" ispfault status
phase "4.5-operator"
api_login
code="$(api_status POST /api/v1/backup/import/preview '{}')"
check "missing multipart backup rejected" test "$code" = "400"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
