#!/bin/sh
# 90 — routerd restart preserves the WG tunnel and applied config.
. "$(dirname "$0")/../lib.sh"
begin "90-routerd-restart-stability"
phase "3-fault"
require "fault: none (routerd restart)" ispfault status
phase "4.5-operator"
require "wg0 up before restart" mr "wg show wg0 | grep -q 'latest handshake'"
require "routerd restart command succeeds" mr "rc-service routerd restart"
sleep 10
require "routerd back up" retry 60 mr "rc-service routerd status | grep -q started"
check "wg0 handshake survives" retry 90 mr "wg show wg0 | grep -q 'latest handshake'"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
health="$(api GET /api/v1/health)"
health_valid="$(echo "$health" | python3 -c 'import json,sys; print(isinstance(json.load(sys.stdin),dict))' 2>/dev/null)"
check "API health remains valid JSON" test "$health_valid" = "True"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
