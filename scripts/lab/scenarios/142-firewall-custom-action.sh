#!/bin/sh
# 142 — Custom firewall actions are restricted to explicit allow or deny.
. "$(dirname "$0")/../lib.sh"
begin "142-firewall-custom-action"
phase "3-fault"
require "fault: none (custom firewall action)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["firewall"]["custom_rules"].append({"id":"lab-rule","name":"lab-rule","action":"accept","direction":"forward","protocol":"tcp","src_ip":"192.168.1.50","dst_port":443,"enabled":True})
print(json.dumps(c))')"
require "unsupported custom action rejected" save_expects_error "$bad"
check "firewall remains fail-closed" check_fw_not_fail_open
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
