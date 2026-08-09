#!/bin/sh
# 28 — LAN subnet change guard: the product deliberately rejects cross-subnet
# LAN moves via the API (live subnet changes are unsupported — recovery
# console only). Verify the change is refused, the LAN stays on 192.168.1.1,
# and nothing is disrupted.
. "$(dirname "$0")/../lib.sh"

begin "28-lan-ip-change"
phase "3-fault"
require "fault: none (config change)" ispfault status

phase "4-mr-runtime"
check "MR up before change" mr "uptime -s | grep -q ."
check "LAN currently 192.168.1.0/24" mr "ip -4 addr show eth1 | grep -q 192.168.1.1"

phase "4.5-operator"
require "live LAN subnet change rejected by API" patch_config_reject "c['lan']['ip_address']='192.168.2.1'; c['lan']['cidr']='192.168.2.0/24'; c['lan']['netmask']='255.255.255.0'" "live LAN subnet changes are unsupported"

phase "4-mr-runtime-2"
check "LAN still on 192.168.1.1" mr "ip -4 addr show eth1 | grep -q 192.168.1.1"
check "no new subnet configured" mr "! ip -4 addr show eth1 | grep -q 192.168.2.1"

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
