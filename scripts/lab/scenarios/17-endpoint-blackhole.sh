#!/bin/sh
# 17 — endpoint-specific blackhole: only the wg0 peer (10.250.0.10:51820)
# becomes unreachable. Internet keeps working; WireGuard must recover when the
# endpoint returns.
. "$(dirname "$0")/../lib.sh"

begin "17-endpoint-blackhole"
phase "3-fault"
require "fault: wg0 endpoint blackholed" ispfault blackhole on 10.250.0.10:51820
sleep 3

phase "4-mr-runtime"
check "PPPoE session still up" check_pppoe
check "internet still works" check_lan_internet
check "local DNS works" check_local_dns

phase "5-lan-client"
check "client unaffected by endpoint blackhole" check_lan_internet

phase "6-revert"
require "fault: endpoint unblackholed" ispfault blackhole off

phase "7-recovery"
require "wg0 handshake returns" retry 150 mr "wg show wg0 | grep -q latest"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
