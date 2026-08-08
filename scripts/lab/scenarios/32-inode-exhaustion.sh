#!/bin/sh
# 32 — Inode exhaustion: fill the inode table on the router's root
# filesystem, then trigger a config save. Router must not crash or fail
# open; after freeing inodes, save must succeed and converge.
. "$(dirname "$0")/../lib.sh"

begin "32-inode-exhaustion"
phase "3-fault"
require "fault: none (filesystem stress)" ispfault status

phase "4-mr-runtime"
check "MR up before inode exhaustion" mr "uptime -s | grep -q ."

phase "4.5-operator"
require "fill inode table" mr "mkdir -p /root/.inode-fill && i=0; while df -i / | awk 'NR==2 && \$5+0 < 95 {exit 0} NR==2 {exit 1}'; do touch /root/.inode-fill/f\$i 2>/dev/null || break; i=\$((i+1)); done; df -i / | tail -1"

phase "4-mr-runtime-2"
api_login
check "local save fails cleanly under inode pressure (no crash)" save_expects_error "$(api GET /api/v1/config)"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "router-applyd still alive" mr "rc-service router-applyd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet

phase "4.5-cleanup"
require "free inodes" mr "rm -rf /root/.inode-fill"

phase "4-mr-runtime-3"
check "save succeeds after inodes freed" mr_save_lease
check "canonical + last-good converge" check_converge

phase "7-recovery"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
