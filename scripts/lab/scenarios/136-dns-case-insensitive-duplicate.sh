#!/bin/sh
# 136 — Static DNS hostnames are unique regardless of letter case.
. "$(dirname "$0")/../lib.sh"
begin "136-dns-case-insensitive-duplicate"
phase "3-fault"
require "fault: none (DNS duplicate name)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["dns"]["records"]=[{"name":"Case.home.arpa","ip":"192.168.1.31"},{"name":"case.home.arpa","ip":"192.168.1.32"}]
print(json.dumps(c))')"
require "case-insensitive DNS duplicate rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
