#!/bin/sh
# 131 — The dynamic DHCP pool cannot contain the router's LAN address.
. "$(dirname "$0")/../lib.sh"
begin "131-dhcp-gateway-in-pool"
phase "3-fault"
require "fault: none (DHCP gateway overlap)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["dhcp"]["range_start"]="192.168.1.1"; c["dhcp"]["range_end"]="192.168.1.20"; print(json.dumps(c))')"
require "gateway inside DHCP pool rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
