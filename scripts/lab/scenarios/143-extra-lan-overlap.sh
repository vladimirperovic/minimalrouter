#!/bin/sh
# 143 — An additional isolated LAN cannot overlap the primary LAN.
. "$(dirname "$0")/../lib.sh"
begin "143-extra-lan-overlap"
phase "3-fault"
require "fault: none (extra LAN overlap)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["firewall"]["extra_lans"].append({"id":"lab-extra","name":"lab-extra","interface":"eth9","cidr":"192.168.1.0/24","router_address":"192.168.1.2/24","dst_ip":"192.168.1.50","dst_port":8080,"protocol":"tcp","allow_from":[c["lan"]["cidr"]],"enabled":True})
print(json.dumps(c))')"
require "overlapping extra LAN rejected" save_expects_error "$bad"
check "firewall remains fail-closed" check_fw_not_fail_open
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
