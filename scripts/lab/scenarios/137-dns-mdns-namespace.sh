#!/bin/sh
# 137 — Static DNS records cannot occupy the reserved .local mDNS namespace.
. "$(dirname "$0")/../lib.sh"
begin "137-dns-mdns-namespace"
phase "3-fault"
require "fault: none (mDNS namespace)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["dns"]["records"].append({"name":"printer.local","ip":"192.168.1.33"}); print(json.dumps(c))')"
require ".local DNS record rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
