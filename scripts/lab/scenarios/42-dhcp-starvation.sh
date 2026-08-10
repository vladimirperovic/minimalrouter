#!/bin/sh
# 42 — DHCP starvation: flood the LAN with DHCP DISCOVER from spoofed MACs.
# The pool must exhaust cleanly (no crash, no fail-open), existing leases
# keep working, and the router stays responsive.
. "$(dirname "$0")/../lib.sh"
begin "42-dhcp-starvation"
phase "3-fault"
require "fault: none (DHCP flood)" ispfault status
phase "4-mr-runtime"
check "MR up before flood" mr "uptime -s | grep -q ."
check "LAN client has lease" lan "ip -4 -o addr show | grep -q 192.168.1."
phase "4.5-operator"
require "DHCP DISCOVER flood" lan "python3 - << 'PY'
import socket, struct, random
s=socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
s.bind(('0.0.0.0', 68))
xid=random.randint(1,2**32)
for i in range(400):
    mac=bytes([0x02, random.getrandbits(8), random.getrandbits(8), random.getrandbits(8), random.getrandbits(8), random.getrandbits(8)])
    pkt=struct.pack('!BBBBIHH', 1,1,6,0, xid+i, 0x8000,0) + struct.pack('!4s4s4s4s', b'\x00'*4,b'\x00'*4,b'\x00'*4,b'\x00'*4) + mac + b'\x00'*192 + b'\x63\x82\x53\x63' + b'\x35\x01\x01'
    s.sendto(pkt, ('255.255.255.255', 67))
print('dhcp-flood-done')
PY"
sleep 3
phase "4-mr-runtime-2"
check "dnsmasq still alive" mr "rc-service dnsmasq status | grep -q started"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN client keeps connectivity" check_lan_internet
phase "4.5-cleanup"
check "router still assigns leases" mr "grep -c . /var/lib/minimalrouter-dhcp/dnsmasq.leases 2>/dev/null | grep -q ."
phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
