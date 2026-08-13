#!/bin/sh
# 141 — Port-forward protocol/port pairs remain unique even while disabled.
. "$(dirname "$0")/../lib.sh"
begin "141-firewall-forward-duplicate"
phase "3-fault"
require "fault: none (port-forward duplicate)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["firewall"]["port_forwards"]=[
 {"id":"lab-pf-a","name":"lab-pf-a","protocol":"tcp","external_port":18081,"internal_ip":"192.168.1.50","internal_port":8080,"enabled":False},
 {"id":"lab-pf-b","name":"lab-pf-b","protocol":"tcp","external_port":18081,"internal_ip":"192.168.1.51","internal_port":8081,"enabled":False}]
print(json.dumps(c))')"
require "duplicate forward endpoint rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
