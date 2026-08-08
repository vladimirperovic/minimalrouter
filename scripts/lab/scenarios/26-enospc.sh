#!/bin/sh
# 26 — ENOSPC / filesystem full: fill the router's root filesystem, then
# trigger a config save. The router must not crash or fail open; after freeing
# space, the save must succeed and converge.
. "$(dirname "$0")/../lib.sh"

begin "26-enospc"
phase "3-fault"
require "fault: none (filesystem stress)" ispfault status

phase "4-mr-runtime"
check "MR up before ENOSPC" mr "uptime -s | grep -q ."

phase "4.5-operator"
require "fill root fs to ~98%" mr "dd if=/dev/zero of=/root/.fill bs=1M count=400 2>/dev/null; df -h / | tail -1 | grep -qE '[9][0-9]%|[1-9][0-9][0-9]%'"

phase "4-mr-runtime-2"
api_login
check "local save fails cleanly under ENOSPC (no crash)" save_expects_error "$(api GET /api/v1/config)"
check "routerd still alive after ENOSPC" mr "rc-service routerd status | grep -q started"
check "router-applyd still alive after ENOSPC" mr "rc-service router-applyd status | grep -q started"
check "firewall still policy-drop under ENOSPC" check_fw_not_fail_open
check "LAN still up under ENOSPC" check_lan_up
check "local DNS still serves under ENOSPC" check_local_dns
check "internet still works under ENOSPC" check_lan_internet

phase "4.5-cleanup"
require "free space" mr "rm -f /root/.fill"

phase "4-mr-runtime-3"
check "save succeeds after space freed" mr_save_lease
check "canonical + last-good converge" check_converge

phase "7-recovery"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
