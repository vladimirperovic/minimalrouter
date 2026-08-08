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
check "dnsmasq lease file exists and non-empty" mr "test -s /var/lib/dnsmasq/dnsmasq.leases 2>/dev/null || test -s /var/lib/misc/dnsmasq.leases 2>/dev/null || ls /var/lib/dnsmasq 2>/dev/null | grep -q lease"

phase "5-lan-client"
check "client holds a 10.77.0.x lease" lan "ip -4 -o addr show | grep -q '192.168.1.'"
check "client renews lease while WAN is down" lan "sudo dhclient -r eth0 2>/dev/null; sleep 1; sudo dhclient eth0 2>/dev/null; sleep 6; ip -4 -o addr show | grep -q '192.168.1.'"

phase "6-revert"
require "fault: carrier restored" ispfault carrier up

phase "7-recovery"
require "PPPoE reconnects" wait_pppoe 150
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
