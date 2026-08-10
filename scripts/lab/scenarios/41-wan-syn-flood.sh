#!/bin/sh
# 41 — WAN SYN flood: a burst of TCP SYN packets against the router's PPPoE
# address must not crash routerd, exhaust conntrack, or flip fail-open.
. "$(dirname "$0")/../lib.sh"
begin "41-wan-syn-flood"
phase "3-fault"
require "fault: none (flood from ISP side)" ispfault status
phase "4-mr-runtime"
check "MR up before flood" mr "uptime -s | grep -q ."
pre_ct="$(mr 'cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null' | tr -d ' \n')"
echo "conntrack before: $pre_ct"
phase "4.5-operator"
require "SYN flood from ISP" isp "python3 - << 'PY'
import socket, time
target=('10.250.0.50', 0)
pids=[]
for w in range(16):
    import subprocess
    pids.append(subprocess.Popen(['sh','-c',
      'for p in $(seq 1 200); do s=$(python3 -c "import socket,sys; s=socket.socket(); s.settimeout(0.05); s.connect((\\"10.250.0.50\\", int(sys.argv[1])))" $p 2>/dev/null); done'], stderr=subprocess.DEVNULL))
for p in pids: p.wait()
print('flood-done')
PY"
phase "4-mr-runtime-2"
check "routerd still alive after flood" mr "rc-service routerd status | grep -q started"
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
