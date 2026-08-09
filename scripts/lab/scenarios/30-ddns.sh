#!/bin/sh
# 30 — DDNS with unreachable provider: enabling dynamic DNS triggers a
# one-shot provider update during the save (verifyDDNSUpdate). In the lab no
# real provider exists, so the save MUST fail cleanly — config unchanged,
# daemon not running, router fully consistent. A successful save here would
# mean the verification gate is broken.
. "$(dirname "$0")/../lib.sh"

begin "30-ddns"
phase "3-fault"
require "fault: none (config change)" ispfault status

phase "4-mr-runtime"
check "MR up before ddns" mr "uptime -s | grep -q ."
check "PPPoE session up" check_pppoe

phase "4.5-operator"
api_login
require "enabling DDNS with unreachable provider fails cleanly" save_expects_error "$(api GET /api/v1/config | python3 -c "
import json,sys
c=json.load(sys.stdin)
c['cloudflare']['ddns_enabled']=True
c['cloudflare']['ddns_provider']='noip'
c['cloudflare']['ddns_username']='lab-ddns-user'
c['cloudflare']['api_token']='lab-ddns-token'
c['cloudflare']['domain']='mr-test.lab.test'
print(json.dumps(c))")"

phase "4-mr-runtime-2"
check "config unchanged — ddns still disabled" config_py_assert 'assert c["cloudflare"]["ddns_enabled"] is False and c["cloudflare"]["domain"] == ""'
check "inadyn not running" mr "! rc-service inadyn status 2>/dev/null | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up" check_lan_up
check "internet still works" check_lan_internet
check "local save still works after rejected DDNS" mr_save_lease

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
