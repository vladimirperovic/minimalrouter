#!/bin/sh
# 40 — WAN IP change: force the PPPoE session to renegotiate with a new IP
# (ISP hands out a different address). The router must re-home the WAN,
# keep NAT/forwarding working, and WireGuard tunnels must recover.
. "$(dirname "$0")/../lib.sh"

begin "40-wan-ip-change"
phase "3-fault"
require "fault: none (WAN renumber)" ispfault status

phase "4-mr-runtime"
check "MR up before WAN change" mr "uptime -s | grep -q ."
oldip="$(mr "ip -4 -o addr show ppp0 | awk '{print \$4}'")"
echo "WAN IP before: $oldip"

phase "4.5-operator"
# force ISP to hand out 10.250.0.99 on the next PPPoE session
# pppd 2.5.2 no longer accepts local-ip/remote-ip in pppoe-server-options;
# the MR ppp0 address is assigned via chap-secrets fixed-ip instead.
isp "sed -i 's/10\.250\.0\.50/10.250.0.99/' /etc/ppp/chap-secrets; cat /etc/ppp/chap-secrets; systemctl restart pppoe-server" >/dev/null 2>&1
sleep 2
mr "rc-service pppoe-wan restart 2>/dev/null || true" >/dev/null 2>&1

phase "4-mr-runtime-2"
require "PPPoE reconnects with new IP" wait_pppoe_ip 10.250.0.99 120
newip="$(mr "ip -4 -o addr show ppp0 | awk '{print \$4}'")"
echo "WAN IP after: $newip"
check "WAN IP actually changed" test "$newip" != "$oldip"
check "internet works with new WAN IP" check_lan_internet
check "LAN still up" check_lan_up
check "firewall still policy-drop" check_fw_not_fail_open

phase "4-mr-runtime-3"
check "wg0 handshake recovers" retry 180 mr "wg show wg0 | grep -q 'latest handshake'"
check "wg1 handshake recovers" retry 180 mr "wg show wg1 | grep -q 'latest handshake'"

phase "4.5-cleanup"
isp "sed -i 's/10\.250\.0\.99/10.250.0.50/' /etc/ppp/chap-secrets; cat /etc/ppp/chap-secrets; systemctl restart pppoe-server" >/dev/null 2>&1
mr "rc-service pppoe-wan restart 2>/dev/null || true" >/dev/null 2>&1
wait_pppoe_ip 10.250.0.50 120 >/dev/null 2>&1 || true

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
