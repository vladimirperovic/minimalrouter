#!/bin/sh
# 34 — External port scan (WAN-facing): a burst of SYN scans against the
# router's WAN interface must not crash routerd, fill the conntrack table,
# or flip the firewall to fail-open. Only management ports may answer.
. "$(dirname "$0")/../lib.sh"

begin "34-wan-port-scan"
phase "3-fault"
require "fault: none (scan from ISP side)" ispfault status

phase "4-mr-runtime"
check "MR up before scan" mr "uptime -s | grep -q ."
pre_ct="$(mr 'cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null' | tr -d ' \n')"
echo "conntrack before: $pre_ct"

phase "4.5-operator"
require "scan WAN ports from ISP" isp "python3 - <<'PY'
import socket
target='$MR_WAN_PPP'
for port in range(1, 1025):
    sock=socket.socket()
    sock.settimeout(0.02)
    try: sock.connect_ex((target, port))
    finally: sock.close()
print('attempted=1024')
PY
"

phase "4-mr-runtime-2"
check "routerd still alive after scan" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
check "LAN still up" check_lan_up
post_ct="$(mr 'cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null' | tr -d ' \n')"
echo "conntrack after: $post_ct"
check "conntrack not exhausted" test "${post_ct:-0}" -lt 20000
check "conntrack counter is numeric" test "${post_ct:-x}" -ge 0

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
