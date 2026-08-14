#!/bin/sh
# 20 — ExtraLAN isolation (10.78.0.0/24): service on 10.78.0.10:8080 reachable
# via the router; the extra LAN must NOT reach the main LAN or WAN.
. "$(dirname "$0")/../lib.sh"

begin "20-extralan-isolation"
phase "3-fault"
require "fault: none (topology-only scenario)" ispfault status

phase "4-mr-runtime"
check "service segment reachable from router" mr "ping -c2 -W2 10.78.0.10 2>&1 | grep -q ' 0% packet loss'"

phase "5-lan-client"
check "extra-LAN service reachable from main LAN via MR" lan "curl -s --max-time 5 http://10.78.0.10:8080/ | grep -q extralan-service-ok"

phase "5-extra-lan-client"
require "ExtraLAN simulator guest is available" sim "echo ready | grep -q ready"
lan_ip="$(lan_client_ipv4)"
case "$lan_ip" in 192.168.1.*) ;; *) finish_scenario 1 ;; esac
extra_if="$(sim "ip -4 -o addr show | awk '\$4 ~ /^10\\.78\\.0\\.10\\// {print \$2; exit}'" | tr -d '\r\n')"
require "ExtraLAN interface discovered" test -n "$extra_if"
cleanup_test_routes() {
  sim "ip route del '$lan_ip/32' via 10.78.0.1 dev '$extra_if' 2>/dev/null || true; ip route del '10.250.0.1/32' via 10.78.0.1 dev '$extra_if' 2>/dev/null || true" >/dev/null 2>&1 || true
}
trap cleanup_test_routes EXIT HUP INT TERM
require "route main-LAN probe through MinimalRouter ExtraLAN" sim "ip route replace '$lan_ip/32' via 10.78.0.1 dev '$extra_if'"
require "route WAN probe through MinimalRouter ExtraLAN" sim "ip route replace '10.250.0.1/32' via 10.78.0.1 dev '$extra_if'"
check "extra LAN cannot reach actual main-LAN client" check_not sim "ping -I 10.78.0.10 -c1 -W2 '$lan_ip'"
check "extra LAN cannot reach ISP WAN address" check_not sim "ping -I 10.78.0.10 -c1 -W2 10.250.0.1"
cleanup_test_routes
trap - EXIT HUP INT TERM

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "firewall remains fail-closed" check_fw_not_fail_open
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
