#!/bin/sh
# 33 — Read-only root filesystem: remount the router root read-only, then
# trigger a config save. The router must keep serving traffic (runtime in
# RAM) and must not crash; after remounting rw, the save must succeed.
. "$(dirname "$0")/../lib.sh"

begin "33-readonly-rootfs"
phase "3-fault"
require "fault: none (filesystem stress)" ispfault status

phase "4-mr-runtime"
check "MR up before readonly" mr "uptime -s | grep -q ."

phase "4.5-operator"
require "remount root ro" mr "mount -o remount,ro / && grep -E ' / ' /proc/mounts | grep -q ' ro,'"

phase "4-mr-runtime-2"
check "internet still works under ro root" check_lan_internet
check "LAN still up" check_lan_up
check "local DNS still serves" check_local_dns
check "firewall still policy-drop" check_fw_not_fail_open
check "routerd still alive" mr "rc-service routerd status | grep -q started"

phase "4.5-cleanup"
require "remount root rw" mr "mount -o remount,rw / && grep -E ' / ' /proc/mounts | grep -q ' rw,'"

phase "4-mr-runtime-3"
check "save succeeds after remount rw" mr_save_lease
check "canonical + last-good converge" check_converge

phase "7-recovery"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
