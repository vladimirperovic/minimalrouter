#!/bin/sh
# 39 — DNS rebinding protection: a public domain that resolves to a private
# address must not be served as-is by the router's DNS; the router must
# either rebind-protect or refuse. LAN resolution of local records keeps
# working.
. "$(dirname "$0")/../lib.sh"

begin "39-dns-rebinding"
phase "3-fault"
require "fault: none (DNS security)" ispfault status

phase "4-mr-runtime"
check "MR up before DNS test" mr "uptime -s | grep -q ."

phase "4.5-operator"
# Configure the lab DNS to answer a public-ish name with a private address
# (the simulated internet DNS server on ISP-LAB can be pointed at 10.250.0.10).
isp "cat > /etc/dnsmasq.d/rebind.conf <<'EOF'
address=/rebind-test.invalid/10.250.0.10
EOF
systemctl restart dnsmasq" >/dev/null 2>&1

phase "4-mr-runtime-2"
# Query through the router's DNS: if rebind protection is on, the answer is
# refused/NXDOMAIN; either way it must NOT be a straight private-IP answer
# forwarded from the upstream.
ans="$(lan "nslookup -timeout=3 rebind-test.invalid 192.168.1.1 2>&1 | grep -A1 Name | tail -1" 2>/dev/null)"
echo "rebind answer: $ans"
check "private address not served to LAN clients" sh -c "! echo \"$ans\" | grep -q '10.250.0.10'"
check "local DNS still serves" check_local_dns
check "internet still works" check_lan_internet

phase "4.5-cleanup"
isp "rm -f /etc/dnsmasq.d/rebind.conf && systemctl restart dnsmasq" >/dev/null 2>&1

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
