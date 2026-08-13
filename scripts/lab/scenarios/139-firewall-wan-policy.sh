#!/bin/sh
# 139 — The default WAN input policy is permanently deny.
. "$(dirname "$0")/../lib.sh"
begin "139-firewall-wan-policy"
phase "3-fault"
require "fault: none (WAN firewall policy)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["firewall"]["default_wan_input_policy"]="allow"; print(json.dumps(c))')"
require "WAN allow policy rejected" save_expects_error "$bad"
check "firewall remains fail-closed" check_fw_not_fail_open
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
