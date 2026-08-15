#!/bin/sh
# 95 — Invalid port values are rejected by config validation.
. "$(dirname "$0")/../lib.sh"
begin "95-config-validation-ports"
phase "3-fault"
require "fault: none (port validation)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
revision="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["system"]["https_port"]=99999
print(json.dumps(c))')"
code="$(api_status PUT /api/v1/config "$bad")"
check "invalid https port rejected with 422" test "$code" = "422"
after="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
check "rejected port did not mutate config" test "$after" = "$revision"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
