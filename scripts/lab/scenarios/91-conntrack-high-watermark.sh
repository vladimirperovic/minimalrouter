#!/bin/sh
# 91 — A real burst of held TCP connections raises conntrack usage, then the
# table returns toward baseline without destabilizing the router.
. "$(dirname "$0")/../lib.sh"
begin "91-conntrack-high-watermark"
phase "3-fault"
require "fault: none (conntrack)" ispfault status
phase "4.5-operator"
baseline="$(mr "cat /proc/sys/net/netfilter/nf_conntrack_count" | tr -d '\r\n')"
require "baseline conntrack count is numeric" sh -c "case '$baseline' in (*[!0-9]*|'') exit 1;; esac"
burst_pid="$(lan "rm -f /tmp/conntrack-burst.count; nohup python3 - <<'PY' >/tmp/conntrack-burst.log 2>&1 &
import socket,time
sockets=[]
for _ in range(120):
    try:
        s=socket.create_connection(('$SIM_INET',80),2)
        sockets.append(s)
    except OSError:
        pass
open('/tmp/conntrack-burst.count','w').write(str(len(sockets)))
time.sleep(25)
for s in sockets:
    s.close()
PY
echo \$!" | tr -d '\r\n')"
require "connection burst process started" sh -c "case '$burst_pid' in (*[!0-9]*|'') exit 1;; esac"
peak="$baseline"
elapsed=0
while [ "$elapsed" -lt 30 ]; do
  current="$(mr "cat /proc/sys/net/netfilter/nf_conntrack_count" | tr -d '\r\n')"
  case "$current" in (*[!0-9]*|'') ;; (*) [ "$current" -gt "$peak" ] && peak="$current" ;; esac
  lan "test -s /tmp/conntrack-burst.count" >/dev/null 2>&1 && [ "$peak" -ge $((baseline+40)) ] && break
  sleep 2
  elapsed=$((elapsed+2))
done
opened="$(lan "cat /tmp/conntrack-burst.count 2>/dev/null" | tr -d '\r\n')"
check "at least 40 simultaneous flows opened" sh -c "case '$opened' in (*[!0-9]*|'') exit 1;; esac; [ '$opened' -ge 40 ]"
check "conntrack rose materially above baseline" test "$peak" -ge $((baseline+40))
require "burst process completed" retry 45 lan "! kill -0 $burst_pid 2>/dev/null"
check "router stays reachable after burst" check_lan_internet
check "conntrack drains after clients close" retry 240 mr "n=\$(cat /proc/sys/net/netfilter/nf_conntrack_count); [ \"\$n\" -le $((baseline+20)) ]"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
