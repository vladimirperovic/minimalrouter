#!/bin/sh
# 32 — Inode exhaustion: fill the inode table on the router's root
# filesystem, then trigger a config save. Router must not crash or fail
# open; after freeing inodes, save must succeed and converge.
. "$(dirname "$0")/../lib.sh"

begin "32-inode-exhaustion"
phase "3-fault"
require "fault: none (filesystem stress)" ispfault status
# a previous aborted run must never leave a fill loop behind
mr "pkill -f inode-fill 2>/dev/null; rm -rf /root/.inode-fill; true" >/dev/null 2>&1

phase "4-mr-runtime"
check "MR up before inode exhaustion" mr "uptime -s | grep -q ."

phase "4.5-operator"
# fill the inode table until <=16 inodes remain: the save needs several new
# files (helper artifacts), so it must fail cleanly while the router itself
# keeps serving from memory. With the base64 transport, `$i`/`$((...))` reach
# the guest unexpanded; xargs batches the touch calls (a bare touch loop over
# 2M files would take hours).
require "fill inode table to <=16 free" mr "mkdir -p /root/.inode-fill && avail=\$(df -i / | awk 'NR==2{print \$4}'); need=\$((avail-16)); seq 1 \$need | sed 's#^#/root/.inode-fill/f#' > /tmp/inode.list && xargs -n 1500 touch < /tmp/inode.list 2>/dev/null; df -i / | awk 'NR==2 && \$4<=16{exit 0} {exit 1}'"

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
