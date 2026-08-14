#!/bin/sh
# 14 — DNS outage: every upstream DNS path stops answering. MR's own dnsmasq
# must keep serving local records; health must degrade, not crash; recovery
# when upstream resolution returns.
. "$(dirname "$0")/../lib.sh"

begin "14-dns-outage"
phase "3-fault"
require "fault: ISP DNS stopped" ispfault dns out
require "fault: upstream DNS egress blocked" isp 'nft flush chain inet labfw blackhole; nft add rule inet labfw blackhole iifname ppp0 udp dport 53 drop; nft add rule inet labfw blackhole iifname ppp0 tcp dport 53 drop'

phase "4-mr-runtime"
sleep 5
api_login
check "local records still resolve" check_local_dns
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up" check_lan_up
check "local save still works" mr_save_lease
health="$(api GET /api/v1/health)"
dns_state="$(echo "$health" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(next((c.get("state","") for c in d.get("checks",[]) if c.get("id")=="dns_dhcp"),""))' 2>/dev/null)"
check "health reports degraded DNS (not crash)" test "$dns_state" = "degraded"

phase "5-lan-client"
check "client lease intact" lan "ip -4 -o addr show | grep -q '192.168.1.'"

phase "6-revert"
require "fault: upstream DNS egress restored" isp "nft flush chain inet labfw blackhole"
require "fault: ISP DNS restored" ispfault dns on

phase "7-recovery"
require "ISP dnsmasq back up" isp "pgrep -x dnsmasq >/dev/null"
check "local DNS still serves" check_local_dns
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
