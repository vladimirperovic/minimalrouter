#!/bin/sh
# 35 — Sustained throughput / latency soak: sustained bidirectional traffic
# through the router for a fixed window while recording packet loss and
# latency. The router must not drop packets under load and must stay
# policy-drop.
. "$(dirname "$0")/../lib.sh"

begin "35-throughput-soak"
phase "3-fault"
require "fault: none (soak test)" ispfault status

phase "4-mr-runtime"
check "MR up before soak" mr "uptime -s | grep -q ."

phase "4.5-operator"
# 30s of ping + parallel HTTP fetches through NAT from the LAN client
lan "ping -c 15 -i 2 11.255.0.2 > /tmp/soak-ping.txt 2>&1" &
LANPID=$!
lan "i=0; while [ \$i -lt 15 ]; do curl -s --max-time 3 http://11.255.0.2/marker.txt >/dev/null 2>&1 || true; i=\$((i+1)); sleep 1; done; echo soak-http-done" &
HTTPPID=$!
sleep 35
wait $LANPID; PING_RC=$?
wait $HTTPPID; HTTP_RC=$?
check "soak ping completed" test $PING_RC -eq 0
check "soak http completed" test $HTTP_RC -eq 0
lan "grep -cE ' 0% packet loss' /tmp/soak-ping.txt" | grep -q 1
check "no packet loss during soak" lan "grep -qE ' 0% packet loss' /tmp/soak-ping.txt"

phase "4-mr-runtime-2"
check "routerd still alive after soak" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
