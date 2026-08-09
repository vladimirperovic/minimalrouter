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
require "scan WAN ports from ISP" isp "for p in \$(seq 1 1024); do echo -n '' > /dev/tcp/10.250.0.50/\$p 2>/dev/null || true; done; echo scan-done"

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
