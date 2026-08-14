#!/bin/sh
# 132 — Two static leases cannot claim the same hardware address.
. "$(dirname "$0")/../lib.sh"
begin "132-dhcp-duplicate-mac"
phase "3-fault"
require "fault: none (DHCP duplicate MAC)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["dhcp"]["static_leases"]=[
 {"id":"lab-a","hostname":"lab-a","mac":"02:00:00:00:AA:01","ip_address":"192.168.1.10"},
 {"id":"lab-b","hostname":"lab-b","mac":"02:00:00:00:AA:01","ip_address":"192.168.1.11"}]
print(json.dumps(c))')"
require "duplicate static-lease MAC rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
