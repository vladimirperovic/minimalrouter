#!/bin/sh
# 33 — Put routerd's canonical data directory on a disposable tmpfs, then
# remount that filesystem read-only.  Remounting the live Alpine root is not a
# valid fault boundary: the kernel rejects it while active services still hold
# root writers.  This exercises the actual SQLite durability boundary without
# interrupting the forwarding plane, and exposes the original state on cleanup.
. "$(dirname "$0")/../lib.sh"
begin "33-readonly-rootfs"
phase "3-fault"
require "fault: none (read-only canonical storage)" ispfault status

STATE_DIR=/var/lib/minimalrouter
STATE_BACKUP=/root/.lab-readonly-routerd-state
restore_state() {
  mr "rc-service routerd stop >/dev/null 2>&1 || true; mountpoint -q $STATE_DIR && mount -o remount,rw $STATE_DIR >/dev/null 2>&1 || true; mountpoint -q $STATE_DIR && umount $STATE_DIR >/dev/null 2>&1 || true; rm -rf $STATE_BACKUP; rc-service routerd start >/dev/null 2>&1 || true" >/dev/null 2>&1 || true
}
trap restore_state EXIT HUP INT TERM
phase "4-mr-runtime"
check "MR up before readonly storage" mr "uptime -s | grep -q ."
phase "4.5-operator"
require "stage canonical state on disposable filesystem" mr "rc-service routerd stop && rm -rf $STATE_BACKUP && mkdir -p $STATE_BACKUP && cp -a $STATE_DIR/. $STATE_BACKUP/ && mount -t tmpfs -o size=32m lab-readonly $STATE_DIR && cp -a $STATE_BACKUP/. $STATE_DIR/ && rc-service routerd start"
require "routerd returns on staged canonical state" retry 60 mr "rc-service routerd status | grep -q started"
api_login
cfg="$(api GET /api/v1/config)"
revision="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
require "remount canonical state read-only" mr "mount -o remount,ro $STATE_DIR && awk '\$2==\"$STATE_DIR\" && \$4 ~ /(^|,)ro(,|\$)/ {ok=1} END{exit !ok}' /proc/mounts"
phase "4-mr-runtime-2"
code="$(api_status PUT /api/v1/config "$cfg")"
check "config write fails closed on read-only canonical storage" sh -c "case '$code' in 500|503|507) exit 0;; *) exit 1;; esac"
after="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
check "failed write leaves revision unchanged" test "$after" = "$revision"
check "runtime traffic survives read-only canonical storage" check_lan_internet
check "firewall still policy-drop" check_fw_not_fail_open
check "routerd still alive" mr "rc-service routerd status | grep -q started"
phase "4.5-cleanup"
require "restore canonical state filesystem" mr "rc-service routerd stop && mount -o remount,rw $STATE_DIR && umount $STATE_DIR && rm -rf $STATE_BACKUP && rc-service routerd start"
trap - EXIT HUP INT TERM
require "routerd returns after canonical-state restore" retry 60 mr "rc-service routerd status | grep -q started"
api_login
require "save succeeds after storage restore" save_config
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
