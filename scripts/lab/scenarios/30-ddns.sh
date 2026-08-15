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
cfg="$(api GET /api/v1/config)"
revision="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
candidate="$(echo "$cfg" | python3 -c "
import json,sys
c=json.load(sys.stdin)
c['cloudflare']['ddns_enabled']=True
c['cloudflare']['ddns_provider']='noip'
c['cloudflare']['ddns_username']='lab-ddns-user'
c['cloudflare']['api_token']='lab-ddns-token'
c['cloudflare']['domain']='mr-test.lab.test'
print(json.dumps(c))")"
code="$(api_status PUT /api/v1/config "$candidate")"
check "unreachable DDNS provider rejects save" sh -c "case '$code' in 4??|5??) exit 0;; *) exit 1;; esac"

phase "4-mr-runtime-2"
check "config unchanged — ddns still disabled" mr "grep -q '\"ddns_enabled\": *false' /var/lib/minimalrouter-applyd/last-good.json 2>/dev/null"
after_revision="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
check "rejected DDNS save did not change revision" test "$after_revision" = "$revision"
check "inadyn not running" mr "! rc-service inadyn status 2>/dev/null | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN still up" check_lan_up
check "internet still works" check_lan_internet
check "ordinary save still works after rejected DDNS" save_config

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
