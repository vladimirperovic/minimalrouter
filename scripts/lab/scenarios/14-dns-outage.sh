#!/bin/sh
# 14 — DNS outage: ISP DNS (10.250.0.1) stops answering. MR's own dnsmasq must
# keep serving local records; health must degrade, not crash; recovery when
# the resolver returns.
. "$(dirname "$0")/../lib.sh"

begin "14-dns-outage"
phase "3-fault"
require "fault: ISP DNS stopped" ispfault dns out

phase "4-mr-runtime"
sleep 5
check "local records still resolve" check_local_dns
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up" check_lan_up
check "local save still works" mr_save_lease
check "health reports degraded DNS (not crash)" mr "curl -sk https://127.0.0.1:8443/api/v1/health 2>/dev/null | grep -q dns_dhcp"

phase "5-lan-client"
check "client lease intact" lan "ip -4 -o addr show | grep -q '192.168.1.'"

phase "6-revert"
require "fault: ISP DNS restored" ispfault dns on

phase "7-recovery"
require "ISP dnsmasq back up" isp "pgrep -x dnsmasq >/dev/null"
check "local DNS still serves" check_local_dns
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
