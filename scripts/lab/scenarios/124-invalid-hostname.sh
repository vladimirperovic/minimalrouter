#!/bin/sh
# 124 — Configuration rejects hostnames containing spaces or shell punctuation.
. "$(dirname "$0")/../lib.sh"
begin "124-invalid-hostname"
phase "3-fault"
require "fault: none (hostname validation)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["system"]["hostname"]="bad host!"; print(json.dumps(c))')"
require "invalid hostname rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
