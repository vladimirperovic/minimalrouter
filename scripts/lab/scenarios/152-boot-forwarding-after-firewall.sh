#!/bin/sh
# 152 — Forwarding must never be enabled before the firewall is loaded.
#
# applyKernelHardening used to set net.ipv4.ip_forward=1 before runNftFile, so a
# cold boot had a window where routing was on and the inet minimalrouter table
# did not exist yet -- the kernel default ACCEPT policy was the only thing
# between WAN and LAN. Forwarding is now switched on by enableIPForwarding()
# only after the policy is proven loaded.
. "$(dirname "$0")/../lib.sh"
begin "152-boot-forwarding-after-firewall"
phase "3-fault"
require "fault: none (boot ordering)" ispfault status

phase "4-mr-runtime"
check "MR up before restart" mr "uptime -s | grep -q ."

phase "4.5-operator"
# Restarting the helper re-runs the full startup reconcile path, which is the
# same code a cold boot executes.
require "router-applyd restart succeeds" mr "rc-service router-applyd restart"
sleep 8

check "firewall table present after reconcile" mr "nft list table inet minimalrouter >/dev/null 2>&1"
check "forwarding enabled after reconcile" mr "test \"\$(sysctl -n net.ipv4.ip_forward)\" = 1"
check "firewall policy-drop after reconcile" check_fw_not_fail_open

# The static ordering guarantee: the generated source must not enable forwarding
# inside applyKernelHardening. A regression there reopens the boot window even
# if the runtime checks above still pass on a warm restart.
check "hardening does not enable forwarding" check_not mr "grep -q 'net.ipv4.ip_forward\", \"1\"' /usr/libexec/minimalrouter/bootstrap/bin/router-applyd-* 2>/dev/null"

check "LAN up after reconcile" check_lan_up
check "local DNS serves after reconcile" check_local_dns
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
