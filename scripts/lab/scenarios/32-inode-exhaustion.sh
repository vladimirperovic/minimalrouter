#!/bin/sh
# 32 — Exhaust inodes in applyd's dedicated state directory using a bounded
# tmpfs, verify persistence fails closed, then reveal the untouched state.
. "$(dirname "$0")/../lib.sh"
begin "32-inode-exhaustion"
phase "3-fault"
require "fault: none (inode stress)" ispfault status
cleanup_inodes() {
  mr "mountpoint -q /var/lib/minimalrouter-applyd && umount /var/lib/minimalrouter-applyd || true; rm -f /root/.lab-last-good.json" >/dev/null 2>&1 || true
}
trap cleanup_inodes EXIT HUP INT TERM
phase "4-mr-runtime"
check "MR up before inode exhaustion" mr "uptime -s | grep -q ."
phase "4.5-operator"
require "mount bounded apply-state filesystem" mr "cp /var/lib/minimalrouter-applyd/last-good.json /root/.lab-last-good.json && mount -t tmpfs -o size=8m,nr_inodes=32 lab-inodes /var/lib/minimalrouter-applyd && cp /root/.lab-last-good.json /var/lib/minimalrouter-applyd/last-good.json"
require "exhaust bounded state-directory inodes" mr "i=0; while touch /var/lib/minimalrouter-applyd/lab-inode-\$i 2>/dev/null; do i=\$((i+1)); done; [ \$i -gt 0 ] && touch /var/lib/minimalrouter-applyd/should-fail 2>/dev/null; [ \$? -ne 0 ]"
phase "4-mr-runtime-2"
api_login
cfg="$(api GET /api/v1/config)"
revision="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
code="$(api_status PUT /api/v1/config "$cfg")"
check "save is rejected while state directory has no inodes" sh -c "case '$code' in 500|503|507) exit 0;; *) exit 1;; esac"
check "router services remain alive" mr "rc-service routerd status | grep -q started && rc-service router-applyd status | grep -q started"
check "firewall remains fail-closed" check_fw_not_fail_open
check "internet remains available" check_lan_internet
phase "4.5-cleanup"
require "unmount bounded inode fault" mr "umount /var/lib/minimalrouter-applyd && rm -f /root/.lab-last-good.json"
trap - EXIT HUP INT TERM
require "save succeeds after inode recovery" save_config
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
