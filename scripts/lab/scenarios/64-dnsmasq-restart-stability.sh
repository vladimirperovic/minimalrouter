#!/bin/sh
# 64 — dnsmasq restart: leases survive and local DNS keeps working.
. "$(dirname "$0")/../lib.sh"
begin "64-dnsmasq-restart-stability"
phase "3-fault"
require "fault: none (dnsmasq)" ispfault status
phase "4-mr-runtime"
check "local DNS works" check_local_dns
phase "4.5-operator"
require "dnsmasq restart succeeds" mr "rc-service dnsmasq restart"
sleep 2
phase "4-mr-runtime-2"
check "local DNS works after restart" check_local_dns
check "LAN client still has lease" lan "ip -4 addr show '$LAN_CLIENT_IF' | grep -q 192.168.1."
check "internet still works" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
