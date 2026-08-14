#!/bin/sh
# 126 — Duplicate management trust networks are rejected.
. "$(dirname "$0")/../lib.sh"
begin "126-trusted-network-duplicate"
phase "3-fault"
require "fault: none (trusted network duplicate)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["trusted_networks"].append(c["trusted_networks"][0]); print(json.dumps(c))')"
require "duplicate trusted network rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
