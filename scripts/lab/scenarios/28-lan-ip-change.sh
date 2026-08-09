#!/bin/sh
# 28 — LAN IP change: move the LAN subnet (192.168.1.0/24 → 192.168.2.0/24) via
# the API and verify the LAN, DHCP and client all follow, then move it back.
# This exercises the transactional apply path end to end.
. "$(dirname "$0")/../lib.sh"

begin "28-lan-ip-change"
phase "3-fault"
require "fault: none (config change)" ispfault status

phase "4-mr-runtime"
check "MR up before change" mr "uptime -s | grep -q ."
check "LAN currently 192.168.1.0/24" mr "ip -4 addr show eth1 | grep -q 192.168.1.1"

phase "4.5-operator"
require "change LAN IP to 192.168.2.1" patch_config "c['lan']['ip_address']='192.168.2.1'; c['lan']['cidr']='192.168.2.0/24'; c['lan']['netmask']='255.255.255.0'"

phase "4-mr-runtime-2"
require "LAN comes up on 192.168.2.1" retry 120 mr "ip -4 addr show eth1 | grep -q 192.168.2.1"
check "old subnet no longer present" mr "! ip -4 addr show eth1 | grep -q 192.168.1.1"

phase "5-lan-client"
require "client gets lease on new subnet" lan "sudo dhclient -r eth0 2>/dev/null; sleep 1; sudo dhclient eth0 2>/dev/null; sleep 8; ip -4 -o addr show | grep -q '192.168.2.'"
check "client internet on new subnet" check_lan_internet

phase "4.5-revert"
require "change LAN IP back to 192.168.1.1" patch_config "c['lan']['ip_address']='192.168.1.1'; c['lan']['cidr']='192.168.1.0/24'; c['lan']['netmask']='255.255.255.0'"

phase "4-mr-runtime-3"
require "LAN back on 192.168.1.1" retry 120 mr "ip -4 addr show eth1 | grep -q 192.168.1.1"

phase "7-recovery"
require "client lease back on original subnet" lan "sudo dhclient -r eth0 2>/dev/null; sleep 1; sudo dhclient eth0 2>/dev/null; sleep 8; ip -4 -o addr show | grep -q '192.168.1.'"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
