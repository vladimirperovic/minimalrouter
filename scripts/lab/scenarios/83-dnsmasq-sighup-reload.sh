#!/bin/sh
# 83 — dnsmasq SIGHUP reload keeps leases and DNS working.
. "$(dirname "$0")/../lib.sh"
begin "83-dnsmasq-sighup-reload"
phase "3-fault"
require "fault: none (sighup)" ispfault status
phase "4.5-operator"
check "local DNS works" check_local_dns
lease_before="$(mr "sha256sum /var/lib/minimalrouter-dhcp/dnsmasq.leases | awk '{print \$1}'")"
require "dnsmasq accepts SIGHUP" retry 15 mr "pid=\$(pgrep -x dnsmasq | head -1); [ -n \"\$pid\" ] && kill -HUP \"\$pid\""
sleep 2
check "DNS works after SIGHUP" check_local_dns
check "client lease intact" lan "ip -4 addr show '$LAN_CLIENT_IF' | grep -q 192.168.1."
lease_after="$(mr "sha256sum /var/lib/minimalrouter-dhcp/dnsmasq.leases | awk '{print \$1}'")"
check "SIGHUP preserved lease database" test -n "$lease_before" -a "$lease_before" = "$lease_after"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
