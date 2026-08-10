#!/bin/sh
# 46 — WireGuard handshake flood: garbage UDP to the WG endpoint. Invalid
# handshakes must not crash wg0 or knock out the legitimate peer.
. "$(dirname "$0")/../lib.sh"
begin "46-wg-handshake-flood"
phase "3-fault"
require "fault: none (WG flood)" ispfault status
phase "4-mr-runtime"
check "MR up before flood" mr "uptime -s | grep -q ."
check "wg0 up" mr "wg show wg0 | grep -q 'interface: wg0'"
phase "4.5-operator"
require "WG handshake flood" isp "python3 - << 'PY'
import socket, os, time
s=socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
buf=os.urandom(1480)
for i in range(3000):
    s.sendto(buf, ('10.250.0.50', 51820))
print('wg-flood-done')
PY"
phase "4-mr-runtime-2"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "wg0 still up" mr "wg show wg0 | grep -q 'interface: wg0'"
check "legit peer handshake survives" retry 60 mr "wg show wg0 | grep -q 'latest handshake'"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
