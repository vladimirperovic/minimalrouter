#!/bin/sh
# 100 — DHCP points the LAN client at Minimal Router, whose resolver answers
# both an authoritative local name and an upstream public name.
. "$(dirname "$0")/../lib.sh"
begin "100-lan-dns-propagation"
phase "3-fault"
require "fault: none (lan dns)" ispfault status
phase "4.5-operator"
check "DHCP installed router as LAN resolver" lan "grep -Eq '^nameserver[[:space:]]+192\\.168\\.1\\.1$' /etc/resolv.conf || resolvectl dns '$LAN_CLIENT_IF' 2>/dev/null | grep -q '192.168.1.1'"
check "router answers authoritative local record" lan "host router.home.arpa 192.168.1.1 2>/dev/null | grep -q '192.168.1.1'"
check "router forwards public DNS query" lan "host example.com 192.168.1.1 2>/dev/null | grep -q 'has address'"
check "LAN client reaches simulated internet" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
