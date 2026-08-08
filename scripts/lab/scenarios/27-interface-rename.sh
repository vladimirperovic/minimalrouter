#!/bin/sh
# 27 — Interface rename: change the LAN interface mapping (eth0 → eth2) via
# the API and verify the LAN comes up on the new interface. This exercises
# the apply state machine's interface reconfiguration path.
. "$(dirname "$0")/../lib.sh"

begin "27-interface-rename"
phase "3-fault"
require "fault: none (config change)" ispfault status

phase "4-mr-runtime"
check "MR up before rename" mr "uptime -s | grep -q ."
check "LAN currently on eth0" mr "ip -4 addr show eth0 2>/dev/null | grep -q 192.168.1.1"

phase "4.5-operator"
require "rename LAN interface eth0 → eth2" patch_config "c['lan']['interface']='eth2'"

phase "4-mr-runtime-2"
require "LAN up on eth2" retry 120 mr "ip -4 addr show eth2 2>/dev/null | grep -q 192.168.1.1"
check "old interface no longer has LAN IP" mr "! ip -4 addr show eth0 2>/dev/null | grep -q 192.168.1.1"

phase "5-lan-client"
require "client gets lease on new LAN interface" lan "sudo dhclient -r eth0 2>/dev/null; sleep 1; sudo dhclient eth0 2>/dev/null; sleep 8; ip -4 -o addr show | grep -q '192.168.1.'"
check "client internet on new interface" check_lan_internet

phase "4.5-revert"
require "rename LAN interface back eth2 → eth0" patch_config "c['lan']['interface']='eth0'"

phase "4-mr-runtime-3"
require "LAN back on eth0" retry 120 mr "ip -4 addr show eth0 2>/dev/null | grep -q 192.168.1.1"

phase "7-recovery"
require "client lease back on eth0" lan "sudo dhclient -r eth0 2>/dev/null; sleep 1; sudo dhclient eth0 2>/dev/null; sleep 8; ip -4 -o addr show | grep -q '192.168.1.'"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
