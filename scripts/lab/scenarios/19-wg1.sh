#!/bin/sh
# 19 — WireGuard wg1 (remote office site): tunneled LAN client traffic to the
# simulated office network, plus recovery after a WAN bounce.
. "$(dirname "$0")/../lib.sh"

begin "19-wg1"
phase "3-fault"
require "fault: PPPoE bounce" ispfault pppoe stop
require "symptom: session dropped" wait_pppoe_down 60
require "fault: PPPoE back" ispfault pppoe start

phase "4-7-recovery"
require "PPPoE reconnects" wait_pppoe 120
require "wg1 handshake re-establishes" retry 180 mr "wg show wg1 | grep -q latest"

phase "4-mr-runtime"
check "office LAN reachable through wg1" mr "ping -c2 -W3 10.79.1.1 2>&1 | grep -q ' 0% packet loss'"
check "office HTTP service reachable from LAN through wg1" lan "curl -s --max-time 5 http://10.79.1.1/index.html | grep -q torture-lab"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
