#!/bin/sh
# 77 — The extra LAN segment cannot reach the LAN: segment isolation stays
# enforced while the extra-LAN service port works.
. "$(dirname "$0")/../lib.sh"
begin "77-firewall-extra-lan-isolation"
phase "3-fault"
require "fault: none (extra-lan)" ispfault status
phase "4.5-operator"
require "ExtraLAN simulator guest is available" sim "echo ready | grep -q ready"
lan_ip="$(lan_client_ipv4)"
case "$lan_ip" in 192.168.1.*) ;; *) finish_scenario 1 ;; esac
extra_if="$(sim "ip -4 -o addr show | awk '\$4 ~ /^10\\.78\\.0\\.10\\// {print \$2; exit}'" | tr -d '\r\n')"
require "ExtraLAN interface discovered" test -n "$extra_if"
cleanup_test_route() {
  sim "ip route del '$lan_ip/32' via 10.78.0.1 dev '$extra_if' 2>/dev/null || true" >/dev/null 2>&1 || true
}
trap cleanup_test_route EXIT HUP INT TERM
require "route LAN probe through MinimalRouter ExtraLAN" sim "ip route replace '$lan_ip/32' via 10.78.0.1 dev '$extra_if'"
check "extra LAN cannot ping actual LAN client" check_not sim "ping -I 10.78.0.10 -c1 -W2 '$lan_ip'"
cleanup_test_route
trap - EXIT HUP INT TERM
check "allowed service remains reachable from LAN" lan "curl -fsS --max-time 5 http://10.78.0.10:8080/ | grep -q extralan-service-ok"
check "firewall remains fail-closed" check_fw_not_fail_open
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
