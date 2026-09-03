#!/bin/sh
# 140 — Enabled port forwards require WireGuard; they can never expose WAN directly.
. "$(dirname "$0")/../lib.sh"
begin "140-firewall-wan-forward"
phase "3-fault"
require "fault: none (WAN port forward)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["wireguard"]["enabled"] = False
c["firewall"]["port_forwards"].append({"id":"lab-pf","name":"lab-pf","protocol":"tcp","external_port":18080,"internal_ip":"192.168.1.50","internal_port":8080,"enabled":True})
print(json.dumps(c))')"
require "enabled forward without WireGuard rejected" save_expects_error "$bad"
check "firewall remains fail-closed" check_fw_not_fail_open
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
