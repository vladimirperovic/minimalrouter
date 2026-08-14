#!/bin/sh
# 17 — endpoint-specific blackhole: only the wg0 peer (10.250.0.10:51820)
# becomes unreachable. Internet keeps working; WireGuard must recover when the
# endpoint returns.
. "$(dirname "$0")/../lib.sh"

begin "17-endpoint-blackhole"
phase "3-fault"
require "fault: wg0 endpoint blackholed" isp 'nft flush chain inet labfw blackhole; nft add rule inet labfw blackhole iifname ppp0 ip daddr 10.250.0.10 udp dport 51820 drop; nft add rule inet labfw blackhole iifname ppp0 ip daddr 10.250.0.10 tcp dport 51820 drop'
sleep 3

phase "4-mr-runtime"
check "PPPoE session still up" check_pppoe
check "internet still works" check_lan_internet
check "local DNS works" check_local_dns

phase "5-lan-client"
check "client unaffected by endpoint blackhole" check_lan_internet

phase "6-revert"
require "fault: endpoint unblackholed" isp "nft flush chain inet labfw blackhole"

phase "7-recovery"
require "wg0 tunnel traffic returns" retry 150 mr "ping -c1 -W3 10.6.0.10 >/dev/null 2>&1"
check "wg0 handshake is recent" check_wg_recent wg0 90
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
