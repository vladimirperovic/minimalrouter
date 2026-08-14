#!/bin/sh
# 40 — Change the fixed PPPoE address in both ISP authentication databases,
# force a reconnect, verify NAT/tunnels, then restore the original address.
. "$(dirname "$0")/../lib.sh"
begin "40-wan-ip-change"
phase "3-fault"
require "fault: none (WAN renumber)" ispfault status
restore_wan_ip() {
  isp "sed -i 's/10\\.250\\.0\\.99$/10.250.0.50/' /etc/ppp/chap-secrets /etc/ppp/pap-secrets 2>/dev/null || true; systemctl restart pppoe-server" >/dev/null 2>&1 || true
  mr "rc-service pppoe-wan restart" >/dev/null 2>&1 || true
}
trap restore_wan_ip EXIT HUP INT TERM
phase "4-mr-runtime"
require "baseline PPPoE address is $MR_WAN_PPP" check_pppoe
oldip="$(mr "ip -4 -o addr show ppp0 | awk '{print \$4}'" | tr -d '\r\n')"
phase "4.5-operator"
require "ISP fixed address changed in CHAP and PAP" isp "test -f /etc/ppp/chap-secrets && test -f /etc/ppp/pap-secrets && sed -i 's/10\\.250\\.0\\.50$/10.250.0.99/' /etc/ppp/chap-secrets /etc/ppp/pap-secrets && grep -q '10.250.0.99$' /etc/ppp/chap-secrets && grep -q '10.250.0.99$' /etc/ppp/pap-secrets"
require "ISP PPPoE service restarted" isp "systemctl restart pppoe-server && systemctl is-active --quiet pppoe-server"
require "router PPPoE client restarted" mr "rc-service pppoe-wan restart"
phase "4-mr-runtime-2"
require "PPPoE reconnects with 10.250.0.99" retry 120 mr "ip -4 -o addr show ppp0 | grep -q '10.250.0.99'"
newip="$(mr "ip -4 -o addr show ppp0 | awk '{print \$4}'" | tr -d '\r\n')"
check "WAN IP actually changed" test "$newip" != "$oldip"
check "internet works with new WAN IP" check_lan_internet
check "LAN still up" check_lan_up
check "firewall still policy-drop" check_fw_not_fail_open
check "wg0 traffic recovers" retry 180 mr "ping -c1 -W3 10.6.0.10 >/dev/null 2>&1"
check "wg0 handshake is recent" check_wg_recent wg0 90
if mr "ip link show wg1 >/dev/null 2>&1"; then
  check "wg1 traffic recovers" retry 180 mr "ping -c1 -W3 10.79.1.1 >/dev/null 2>&1"
  check "wg1 handshake is recent" check_wg_recent wg1 90
fi
phase "4.5-cleanup"
require "ISP fixed address restored" isp "sed -i 's/10\\.250\\.0\\.99$/10.250.0.50/' /etc/ppp/chap-secrets /etc/ppp/pap-secrets && grep -q '10.250.0.50$' /etc/ppp/chap-secrets && grep -q '10.250.0.50$' /etc/ppp/pap-secrets && systemctl restart pppoe-server"
require "router reconnects to original WAN IP" mr "rc-service pppoe-wan restart"
require "original PPPoE address returns" wait_pppoe 120
trap - EXIT HUP INT TERM
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
