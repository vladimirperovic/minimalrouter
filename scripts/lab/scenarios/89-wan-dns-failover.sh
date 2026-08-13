#!/bin/sh
# 89 — Local authoritative DNS remains available while the isolated ISP DNS
# service is down; the ISP service is restored on every exit path.
. "$(dirname "$0")/../lib.sh"
begin "89-wan-dns-failover"
phase "3-fault"
require "fault: none (dns failover)" ispfault status
restore_isp_dns() { isp "systemctl start dnsmasq" >/dev/null 2>&1 || true; }
trap restore_isp_dns EXIT HUP INT TERM
phase "4.5-operator"
require "ISP DNS stopped" isp "systemctl stop dnsmasq && ! systemctl is-active --quiet dnsmasq"
check "local DNS still resolves through router" check_local_dns
check "IP connectivity survives DNS outage" check_lan_internet
phase "4.5-cleanup"
require "ISP DNS restarted" isp "systemctl start dnsmasq && systemctl is-active --quiet dnsmasq"
trap - EXIT HUP INT TERM
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
