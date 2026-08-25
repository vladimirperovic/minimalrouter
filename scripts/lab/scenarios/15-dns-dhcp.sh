#!/bin/sh
# 15 — DNS records + DHCP: local records resolve, DHCP lease/renewal works,
# and DNS survives independent of the WAN.
. "$(dirname "$0")/../lib.sh"

begin "15-dns-dhcp"
phase "3-fault"
require "fault: ISP access NIC carrier down (WAN down during DHCP/DNS test)" ispfault carrier down
require "symptom: session dropped" wait_pppoe_down 60

phase "4-mr-runtime"
check "local record router.home.arpa resolves" check_local_dns

phase "5-lan-client"
check "client holds a 192.168.1.x lease" lan "ip -4 -o addr show | grep -q '192.168.1.'"
check "client obtains a fresh lease while WAN is down" lan_dhcp_renew
check "dnsmasq durable lease file exists and is non-empty" retry 30 mr "test -s /var/lib/minimalrouter-dhcp/dnsmasq.leases"

phase "6-revert"
require "fault: carrier restored" ispfault carrier up

phase "7-recovery"
require "PPPoE reconnects" wait_pppoe 150
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
