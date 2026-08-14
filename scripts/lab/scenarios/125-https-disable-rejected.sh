#!/bin/sh
# 125 — The management API cannot be saved with HTTPS disabled.
. "$(dirname "$0")/../lib.sh"
begin "125-https-disable-rejected"
phase "3-fault"
require "fault: none (HTTPS invariant)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["system"]["https_enabled"]=False; print(json.dumps(c))')"
require "HTTPS disable rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
