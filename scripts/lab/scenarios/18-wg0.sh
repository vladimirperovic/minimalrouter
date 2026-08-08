#!/bin/sh
# 18 — WireGuard wg0 (server): handshake, tunnel traffic, and recovery after
# a PPPoE bounce.
. "$(dirname "$0")/../lib.sh"

begin "18-wg0"
phase "3-fault"
require "fault: PPPoE bounce to force wg0 endpoint churn" ispfault pppoe stop
require "symptom: session dropped" wait_pppoe_down 60
require "fault: PPPoE back" ispfault pppoe start

phase "4-7-recovery"
require "PPPoE reconnects" wait_pppoe 120

phase "4-mr-runtime"
require "wg0 tunnel re-establishes after WAN bounce" retry 180 mr "wg show wg0 | grep -q latest"
check "wg0 tunnel traffic flows" mr "ping -c2 -W3 10.6.0.10 2>&1 | grep -q ' 0% packet loss'"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
