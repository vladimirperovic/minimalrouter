#!/bin/sh
# 57 — WAN port-forward protection: an enabled DNAT rule is rejected because
# WireGuard is the appliance's only permitted external entry point.
. "$(dirname "$0")/../lib.sh"
begin "57-firewall-port-forward"
phase "3-fault"
require "fault: none (port forward)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c.setdefault("firewall",{}).setdefault("port_forwards",[]).append({"id":"lab-pf","name":"lab-fwd","protocol":"tcp","external_port":18080,"internal_ip":"192.168.1.187","internal_port":8080,"enabled":True})
print(json.dumps(c))')"
code="$(api_status PUT /api/v1/config "$new")"
require "enabled WAN forward rejected" test "$code" = "422"
check "DNAT rule never installed" check_not mr "nft list ruleset 2>/dev/null | grep -q 18080"
check "WAN remains fail-closed" check_fw_not_fail_open
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
