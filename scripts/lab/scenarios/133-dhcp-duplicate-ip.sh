#!/bin/sh
# 133 — Two static leases cannot claim the same IPv4 address.
. "$(dirname "$0")/../lib.sh"
begin "133-dhcp-duplicate-ip"
phase "3-fault"
require "fault: none (DHCP duplicate IP)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["dhcp"]["static_leases"]=[
 {"id":"lab-a","hostname":"lab-a","mac":"02:00:00:00:AA:01","ip_address":"192.168.1.10"},
 {"id":"lab-b","hostname":"lab-b","mac":"02:00:00:00:AA:02","ip_address":"192.168.1.10"}]
print(json.dumps(c))')"
require "duplicate static-lease IP rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
