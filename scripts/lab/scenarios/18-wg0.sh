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
require "wg0 tunnel traffic returns after WAN bounce" retry 180 mr "ping -c1 -W3 10.6.0.10 >/dev/null 2>&1"
check "wg0 handshake is recent" check_wg_recent wg0 90
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
