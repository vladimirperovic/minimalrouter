#!/bin/sh
# 29 — Squid content proxy: enable the router's Squid proxy, verify LAN
# clients can fetch through it, then disable it.
. "$(dirname "$0")/../lib.sh"

begin "29-squid"
phase "3-fault"
require "fault: none (config change)" ispfault status

phase "4-mr-runtime"
check "MR up before squid" mr "uptime -s | grep -q ."
check "internet before squid" check_lan_internet

phase "4.5-operator"
require "enable squid" patch_config "c['squid_proxy']['enabled']=True; c['squid_proxy']['username']='labproxy'; c['squid_proxy']['password']='lab-proxy-pass-2026'; c['squid_proxy']['port']=3128"

phase "4-mr-runtime-2"
require "squid listening on 3128" retry 120 mr "ss -tlnp | grep -q :3128"

phase "5-lan-client"
check "client fetches through squid" lan "curl -s --max-time 10 -x http://labproxy:lab-proxy-pass-2026@192.168.1.1:3128 http://$SIM_INET/marker.txt | grep -q torture-lab"
check "squid logged the request" mr "grep -q 'TCP_MISS' /var/log/squid/access.log 2>/dev/null"

phase "4.5-revert"
require "disable squid" patch_config "c['squid_proxy']['enabled']=False"

phase "4-mr-runtime-3"
require "squid stopped" retry 120 mr "! ss -tlnp | grep -q :3128"

phase "7-recovery"
check "internet still works after squid disable" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
