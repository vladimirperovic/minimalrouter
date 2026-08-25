#!/bin/sh
# 13 — MTU problems: ISP forces MTU 1400 on the PPPoE link. PPPoE must still
# negotiate and LAN clients must still reach the internet (MSS clamp).
. "$(dirname "$0")/../lib.sh"

begin "13-mtu-issues"
phase "3-fault"
require "fault: server MTU forced to 1400" ispfault mtu 1400

phase "4-5-runtime"
require "PPPoE renegotiates under MTU 1400" wait_pppoe 120
check "server MTU/MRU fault remains active" isp "grep -qx 'mtu 1400' /etc/ppp/pppoe-server-options && grep -qx 'mru 1400' /etc/ppp/pppoe-server-options"
check "LAN client internet works with small MTU" check_lan_internet
check "local DNS works" check_local_dns

phase "6-revert"
require "fault: MTU restored to 1492" ispfault mtu 1492

phase "7-recovery"
require "PPPoE back on standard MTU" wait_pppoe 120
check "negotiated MTU is 1492" mr "ip link show ppp0 | grep -o 'mtu 1492'"
check "LAN client internet back" check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
