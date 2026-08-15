#!/bin/sh
# 129 — WAN MTU values below the supported IPv6-safe floor are rejected.
. "$(dirname "$0")/../lib.sh"
begin "129-wan-mtu-lower-bound"
phase "3-fault"
require "fault: none (WAN MTU)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["wan"]["mtu"]=1279; print(json.dumps(c))')"
require "undersized WAN MTU rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
