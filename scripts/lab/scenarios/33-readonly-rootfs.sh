#!/bin/sh
# 33 — Put routerd's canonical data directory on a disposable tmpfs, then put
# a read-only bind over it. Remounting a live filesystem read-only is rejected
# while a service has active writers. The bind gives the same process-visible
# read-only storage contract without weakening that kernel safety invariant.
# routerd must then refuse startup; the already-applied forwarding plane stays
# active, and cleanup reveals the untouched original state.
. "$(dirname "$0")/../lib.sh"
begin "33-readonly-rootfs"
phase "3-fault"
require "fault: none (read-only canonical storage)" ispfault status

STATE_DIR=/var/lib/minimalrouter
STATE_BACKUP=/root/.lab-readonly-routerd-state
restore_state() {
  mr "rc-service routerd stop >/dev/null 2>&1 || true; mountpoint -q $STATE_DIR && umount $STATE_DIR >/dev/null 2>&1 || true; mountpoint -q $STATE_DIR && umount $STATE_DIR >/dev/null 2>&1 || true; rm -rf $STATE_BACKUP; rc-service routerd start >/dev/null 2>&1 || true" >/dev/null 2>&1 || true
}
trap restore_state EXIT HUP INT TERM
phase "4-mr-runtime"
check "MR up before readonly storage" mr "uptime -s | grep -q ."
phase "4.5-operator"
require "stage canonical state on disposable filesystem" mr "rc-service routerd stop && rm -rf $STATE_BACKUP && mkdir -p $STATE_BACKUP && cp -a $STATE_DIR/. $STATE_BACKUP/ && mount -t tmpfs -o size=32m lab-readonly $STATE_DIR && cp -a $STATE_BACKUP/. $STATE_DIR/"
require "bind canonical state read-only" mr "mount --bind $STATE_DIR $STATE_DIR && mount -o remount,bind,ro $STATE_DIR && awk '\$2==\"$STATE_DIR\" && \$4 ~ /(^|,)ro(,|\$)/ {ok=1} END{exit !ok}' /proc/mounts && ! touch $STATE_DIR/.lab-readonly-probe"
phase "4-mr-runtime-2"
check "routerd refuses read-only canonical storage" mr "! timeout 30 su routerd -s /bin/sh -c 'MINIMALROUTER_DATA_DIR=$STATE_DIR /usr/bin/routerd >/dev/null 2>&1'"
check "runtime traffic survives read-only canonical storage" check_lan_internet
check "firewall still policy-drop" check_fw_not_fail_open
check "privileged applyd remains alive" mr "rc-service router-applyd status | grep -q started"
phase "4.5-cleanup"
require "restore canonical state filesystem" mr "umount $STATE_DIR && umount $STATE_DIR && rm -rf $STATE_BACKUP && rc-service routerd start"
trap - EXIT HUP INT TERM
require "routerd returns after canonical-state restore" retry 60 mr "rc-service routerd status | grep -q started"
api_login
require "save succeeds after storage restore" save_config
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
