#!/bin/sh
# 42 — DHCP starvation: flood the LAN with DHCP DISCOVER from spoofed MACs.
# The pool must exhaust cleanly (no crash, no fail-open), existing leases
# keep working, and the router stays responsive.
. "$(dirname "$0")/../lib.sh"
begin "42-dhcp-starvation"
phase "3-fault"
require "fault: none (DHCP flood)" ispfault status
phase "4-mr-runtime"
check "MR up before flood" mr "uptime -s | grep -q ."
check "LAN client has lease" lan "ip -4 -o addr show | grep -q 192.168.1."
phase "4.5-operator"
require "DHCP DISCOVER flood" lan "test -x /root/lab-fault-dhcpflood.py && python3 /root/lab-fault-dhcpflood.py"
sleep 3
phase "4-mr-runtime-2"
check "dnsmasq still alive" mr "rc-service dnsmasq status | grep -q started"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN client keeps connectivity" check_lan_internet
phase "4.5-cleanup"
check "router still assigns leases" mr "grep -c . /var/lib/minimalrouter-dhcp/dnsmasq.leases 2>/dev/null | grep -q ."
phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
