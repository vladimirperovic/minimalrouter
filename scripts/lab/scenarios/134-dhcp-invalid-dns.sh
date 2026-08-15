#!/bin/sh
# 134 — DHCP upstream resolvers must be valid IPv4 addresses.
. "$(dirname "$0")/../lib.sh"
begin "134-dhcp-invalid-dns"
phase "3-fault"
require "fault: none (DHCP resolver)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["dhcp"]["dns_servers"]=["999.1.1.1"]; print(json.dumps(c))')"
require "invalid DHCP resolver rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
