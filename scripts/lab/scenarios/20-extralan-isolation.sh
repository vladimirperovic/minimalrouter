#!/bin/sh
# 20 — ExtraLAN isolation (10.78.0.0/24): service on 10.78.0.10:8080 reachable
# via the router; the extra LAN must NOT reach the main LAN or WAN.
. "$(dirname "$0")/../lib.sh"

begin "20-extralan-isolation"
phase "3-fault"
require "fault: none (topology-only scenario)" ispfault status

phase "4-mr-runtime"
check "service segment reachable from router" mr "ping -c2 -W2 10.78.0.10 2>&1 | grep -q ' 0% packet loss'"

phase "5-lan-client"
check "extra-LAN service reachable from main LAN via MR" lan "curl -s --max-time 5 http://10.78.0.10:8080/ | grep -q extralan-service-ok"

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
