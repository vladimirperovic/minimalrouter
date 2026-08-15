#!/bin/sh
# 44 — DNS query flood: high-QPS queries to the router's dnsmasq. The router
# must keep answering, stay stable, and converge.
. "$(dirname "$0")/../lib.sh"
begin "44-dns-high-qps"
phase "3-fault"
require "fault: none (DNS flood)" ispfault status
phase "4-mr-runtime"
check "MR up before flood" mr "uptime -s | grep -q ."
phase "4.5-operator"
require "DNS query flood" lan "i=0; while [ \$i -lt 300 ]; do j=0; while [ \$j -lt 25 ] && [ \$i -lt 300 ]; do host -W 1 router.home.arpa 192.168.1.1 >/dev/null 2>&1 & i=\$((i+1)); j=\$((j+1)); done; wait; done; echo dns-flood-done"
phase "4-mr-runtime-2"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "dnsmasq still alive" mr "rc-service dnsmasq status | grep -q started"
check "local DNS still resolves" check_local_dns
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
