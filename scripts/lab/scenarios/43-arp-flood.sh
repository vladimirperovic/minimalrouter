#!/bin/sh
# 43 — ARP flood: a storm of spoofed ARP requests on the LAN must not wedge
# the router's neighbor table or the data plane; real LAN hosts stay reachable.
. "$(dirname "$0")/../lib.sh"
begin "43-arp-flood"
phase "3-fault"
require "fault: none (ARP flood)" ispfault status
phase "4-mr-runtime"
check "MR up before flood" mr "uptime -s | grep -q ."
phase "4.5-operator"
require "ARP flood on LAN" lan "test -x /root/lab-fault-arpflood.py && python3 /root/lab-fault-arpflood.py"
phase "4-mr-runtime-2"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up" retry 30 check_lan_up
check "internet still works" check_lan_internet
neighbors="$(mr 'ip neigh show | wc -l' | tr -d ' \n')"
check "neighbor table remains bounded" test "${neighbors:-99999}" -lt 512
phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
