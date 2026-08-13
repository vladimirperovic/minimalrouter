#!/bin/sh
# 135 — DHCP lease durations shorter than one minute are rejected.
. "$(dirname "$0")/../lib.sh"
begin "135-dhcp-lease-duration"
phase "3-fault"
require "fault: none (DHCP lease time)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["dhcp"]["lease_time"]="30s"; print(json.dumps(c))')"
require "short DHCP lease rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
