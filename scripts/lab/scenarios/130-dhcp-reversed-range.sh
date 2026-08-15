#!/bin/sh
# 130 — DHCP range start cannot be greater than range end.
. "$(dirname "$0")/../lib.sh"
begin "130-dhcp-reversed-range"
phase "3-fault"
require "fault: none (DHCP ordering)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["dhcp"]["range_start"]="192.168.1.220"; c["dhcp"]["range_end"]="192.168.1.200"; print(json.dumps(c))')"
require "reversed DHCP range rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
