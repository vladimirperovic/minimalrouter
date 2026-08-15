#!/bin/sh
# 128 — WAN and LAN may never reuse the same Linux interface.
. "$(dirname "$0")/../lib.sh"
begin "128-wan-lan-interface-collision"
phase "3-fault"
require "fault: none (interface collision)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["wan"]["interface"]=c["lan"]["interface"]; print(json.dumps(c))')"
require "WAN/LAN collision rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
