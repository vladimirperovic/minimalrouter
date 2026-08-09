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
# burst of SYN scans from the ISP side against the router's current WAN
# address; probes run in parallel and are bounded by nc's 1s timeout
wanip="$(mr "ip -4 -o addr show ppp0 | awk '{print \$4}'")"
echo "scan target: $wanip"
require "scan WAN ports from ISP" isp "for p in \$(seq 1 1024); do nc -w 1 -z $wanip \$p >/dev/null 2>&1 & done; wait; echo scan-done"

phase "4-mr-runtime-2"
check "routerd still alive after scan" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
check "LAN still up" check_lan_up
post_ct="$(mr 'cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null' | tr -d ' \n')"
echo "conntrack after: $post_ct"
check "conntrack not exhausted" test "${post_ct:-0}" -lt 20000

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
