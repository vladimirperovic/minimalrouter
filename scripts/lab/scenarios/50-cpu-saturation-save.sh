#!/bin/sh
# 50 — CPU saturation: with the router's CPU pegged, a config save must still
# complete, routerd must stay responsive, and the router must recover after
# the load is removed.
. "$(dirname "$0")/../lib.sh"
begin "50-cpu-saturation-save"
phase "3-fault"
require "fault: none (CPU stress)" ispfault status
phase "4-mr-runtime"
check "MR up before stress" mr "uptime -s | grep -q ."
phase "4.5-operator"
nohup mr "sha256sum /dev/zero >/dev/null 2>&1" >/dev/null 2>&1 &
sleep 3
require "saturate router CPU" mr "pgrep sha256sum | wc -l | grep -q [1-9]"
phase "4-mr-runtime-2"
check "routerd still responsive" retry 60 mr "rc-service routerd status | grep -q started"
api_login
cfg="$(api GET /api/v1/config)" || finish_scenario 1
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["dhcp"]["lease_time"]="3h"
print(json.dumps(c))')"
check "config save succeeds under load" api PUT /api/v1/config "$new"
check "firewall still policy-drop" check_fw_not_fail_open
phase "4.5-cleanup"
require "remove CPU load" mr "pkill sha256sum 2>/dev/null; sleep 2; pgrep sha256sum | wc -l | grep -q ^0"
phase "7-recovery"
check "canonical + last-good converge" retry 60 check_converge
check "internet works after stress" check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
