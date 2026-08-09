#!/bin/sh
# 27 — Interface rename guard: the product deliberately rejects live LAN
# interface swaps via the API (self-lockout protection — use the recovery
# console instead). Verify the change is refused, the LAN stays on eth1, and
# nothing is disrupted.
. "$(dirname "$0")/../lib.sh"

begin "27-interface-rename"
phase "3-fault"
require "fault: none (config change)" ispfault status

phase "4-mr-runtime"
check "MR up before rename" mr "uptime -s | grep -q ."
check "LAN currently on eth1" mr "ip -4 addr show eth1 2>/dev/null | grep -q 192.168.1.1"

phase "4.5-operator"
require "live LAN interface change rejected by API" patch_config_reject "c['lan']['interface']='eth3'" "live LAN interface changes are unsupported"

phase "4-mr-runtime-2"
check "LAN still on eth1" mr "ip -4 addr show eth1 2>/dev/null | grep -q 192.168.1.1"
check "eth3 has no LAN IP" mr "! ip -4 addr show eth3 2>/dev/null | grep -q 192.168.1.1"

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
