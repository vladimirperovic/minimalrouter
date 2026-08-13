#!/bin/sh
# 26 — Fill the router root filesystem to ENOSPC, verify a config write fails
# closed, free the exact lab files, and prove persistence recovers.
. "$(dirname "$0")/../lib.sh"
begin "26-enospc"
phase "3-fault"
require "fault: none (filesystem stress)" ispfault status
cleanup_enospc() { mr "rm -f /root/.lab-enospc-fill /root/.lab-enospc-tail" >/dev/null 2>&1 || true; }
trap cleanup_enospc EXIT HUP INT TERM
phase "4-mr-runtime"
check "MR up before ENOSPC" mr "uptime -s | grep -q ."
phase "4.5-operator"
require "fill root filesystem above 98 percent" mr "avail=\$(df -Pk / | awk 'NR==2{print \$4}'); target=\$((avail-2048)); [ \$target -gt 0 ]; fallocate -l \${target}K /root/.lab-enospc-fill 2>/dev/null || dd if=/dev/zero of=/root/.lab-enospc-fill bs=1024 count=\$target 2>/dev/null; dd if=/dev/zero of=/root/.lab-enospc-tail bs=1M 2>/dev/null || true; used=\$(df -Pk / | awk 'NR==2{gsub(/%/,\"\",\$5); print \$5}'); [ \$used -ge 98 ]"
phase "4-mr-runtime-2"
api_login
cfg="$(api GET /api/v1/config)"
revision="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
code="$(api_status PUT /api/v1/config "$cfg")"
check "config write fails closed under ENOSPC" sh -c "case '$code' in 500|503|507) exit 0;; *) exit 1;; esac"
after="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
check "failed write leaves canonical revision unchanged" test "$after" = "$revision"
check "routerd still alive after ENOSPC" mr "rc-service routerd status | grep -q started"
check "router-applyd still alive after ENOSPC" mr "rc-service router-applyd status | grep -q started"
check "firewall still policy-drop under ENOSPC" check_fw_not_fail_open
check "LAN and internet survive ENOSPC" check_lan_internet
phase "4.5-cleanup"
require "free lab fill files" mr "rm -f /root/.lab-enospc-fill /root/.lab-enospc-tail && test \$(df -Pk / | awk 'NR==2{print \$4}') -gt 8192"
trap - EXIT HUP INT TERM
phase "4-mr-runtime-3"
require "save succeeds after space freed" save_config
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
