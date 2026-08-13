#!/bin/sh
# 77 — The extra LAN segment cannot reach the LAN: segment isolation stays
# enforced while the extra-LAN service port works.
. "$(dirname "$0")/../lib.sh"
begin "77-firewall-extra-lan-isolation"
phase "3-fault"
require "fault: none (extra-lan)" ispfault status
phase "4.5-operator"
require "ExtraLAN simulator guest is available" sim "echo ready | grep -q ready"
check "extra LAN cannot ping LAN client" check_not sim "ping -c1 -W2 192.168.1.187"
check "allowed service remains reachable from LAN" lan "curl -fsS --max-time 5 http://10.78.0.10:8080/ | grep -q extralan-service-ok"
check "firewall remains fail-closed" check_fw_not_fail_open
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
