#!/bin/sh
# 67 — Unsupported features are rejected at the API: DNS-over-HTTPS must be
# refused with a clear error, never silently accepted.
. "$(dirname "$0")/../lib.sh"
begin "67-doh-rejected"
phase "3-fault"
require "fault: none (doh)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c.setdefault("dhcp",{})["dns_enabled"]=True
print(json.dumps(c))')"
require "DoH config rejected" save_expects_error "$new"
phase "4-mr-runtime-2"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "config unchanged" mr "grep -q 'dns_enabled.:false' /var/lib/minimalrouter-applyd/last-good.json"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
