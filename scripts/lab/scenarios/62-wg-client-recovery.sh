#!/bin/sh
# 62 — WG client (wg1) outage and recovery: when the remote tunnel drops the
# router keeps serving and the tunnel re-establishes.
. "$(dirname "$0")/../lib.sh"
begin "62-wg-client-recovery"
phase "3-fault"
require "fault: none (wg1)" ispfault status
phase "4-mr-runtime"
check "wg1 up before outage" mr "wg show wg1 | grep -q 'latest handshake'"
phase "4.5-operator"
restore_wg1() { sim "ip link del wg1 2>/dev/null; systemctl start wg-quick@wg1" >/dev/null 2>&1 || true; }
trap restore_wg1 EXIT HUP INT TERM
require "remote wg1 stopped" sim "systemctl stop wg-quick@wg1"
sleep 3
check "wg1 handshake lost" check_not mr "wg show wg1 | grep -q 'latest handshake'"
phase "4-mr-runtime-2"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "internet still works" check_lan_internet
phase "4.5-cleanup"
require "remote wg1 restarted" sim "ip link del wg1 2>/dev/null; systemctl start wg-quick@wg1"
trap - EXIT HUP INT TERM
phase "7-recovery"
check "wg1 handshake recovers" retry 120 mr "wg show wg1 | grep -q 'latest handshake'"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
