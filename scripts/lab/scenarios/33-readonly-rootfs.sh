#!/bin/sh
# 33 — Read-only root filesystem: make the router's persistent state
# unwritable (whole-root remount ro when the kernel allows it, otherwise a
# deterministic read-only bind mount of the state directories), then trigger
# a config save. The router must keep serving traffic (runtime in RAM) and
# must not crash; after remounting rw, the save must succeed.
. "$(dirname "$0")/../lib.sh"

begin "33-readonly-rootfs"
phase "3-fault"
require "fault: none (filesystem stress)" ispfault status

phase "4-mr-runtime"
check "MR up before readonly" mr "uptime -s | grep -q ."

phase "4.5-operator"
# A live router's root is usually too busy for a whole-root remount-ro
# (EBUSY), so fall back to a read-only bind mount of the state directories —
# the same failure surface (persistent state unwritable) without the flaky
# kernel dependency.
RO_MODE=""
if mr "mount -o remount,ro / 2>/dev/null && grep -E ' / ' /proc/mounts | grep -q ' ro,'"; then
  RO_MODE=root
  echo "root remounted read-only"
else
  require "state dirs read-only (bind)" mr "mount --bind /var/lib/minimalrouter-applyd /var/lib/minimalrouter-applyd && mount -o remount,ro,bind /var/lib/minimalrouter-applyd && mount --bind /var/lib/minimalrouter /var/lib/minimalrouter && mount -o remount,ro,bind /var/lib/minimalrouter && ! touch /var/lib/minimalrouter-applyd/.rotest 2>/dev/null"
  RO_MODE=bind
fi

phase "4-mr-runtime-2"
check "internet still works under ro root" check_lan_internet
check "LAN still up" check_lan_up
check "local DNS still serves" check_local_dns
check "firewall still policy-drop" check_fw_not_fail_open
check "routerd still alive" mr "rc-service routerd status | grep -q started"

phase "4.5-cleanup"
if [ "$RO_MODE" = root ]; then
  require "remount root rw" mr "mount -o remount,rw / && grep -E ' / ' /proc/mounts | grep -q ' rw,'"
else
  require "remount state dirs rw" mr "mount -o remount,rw,bind /var/lib/minimalrouter-applyd && mount -o remount,rw,bind /var/lib/minimalrouter && touch /var/lib/minimalrouter-applyd/.rotest 2>/dev/null && rm -f /var/lib/minimalrouter-applyd/.rotest"
fi

phase "4-mr-runtime-3"
check "save succeeds after remount rw" mr_save_lease
check "canonical + last-good converge" check_converge

phase "7-recovery"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
