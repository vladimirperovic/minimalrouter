#!/bin/sh
# 43 — ARP flood: a storm of spoofed ARP requests on the LAN must not wedge
# the router's neighbor table or the data plane; real LAN hosts stay reachable.
. "$(dirname "$0")/../lib.sh"
begin "43-arp-flood"
phase "3-fault"
require "fault: none (ARP flood)" ispfault status
phase "4-mr-runtime"
check "MR up before flood" mr "uptime -s | grep -q ."
phase "4.5-operator"
require "ARP flood on LAN" lan "python3 - << 'PY'
import socket, struct, random, time
s=socket.socket(socket.AF_PACKET, socket.SOCK_RAW)
s.bind(('eth0', 0x0806))
src_mac=bytes.fromhex('02' + ''.join('%02x'%random.getrandbits(8) for _ in range(5)))
for i in range(1500):
    src_ip='192.168.1.%d' % random.randint(2,250)
    dst_ip='192.168.1.%d' % random.randint(2,250)
    sha=src_mac; spa=socket.inet_aton(src_ip); tha=src_mac; tpa=socket.inet_aton(dst_ip)
    arp=struct.pack('!HHBBH6s4s6s4s', 1,0x0800,6,4,1,sha,spa,tha,tpa)
    frame=b'\xff\xff\xff\xff\xff\xff'+sha+struct.pack('!H',0x0806)+arp
    s.send(frame)
print('arp-flood-done')
PY"
phase "4-mr-runtime-2"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up" check_lan_up
check "internet still works" check_lan_internet
check "neighbor table sane" mr "ip neigh show | grep -c . | grep -q ."
phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
