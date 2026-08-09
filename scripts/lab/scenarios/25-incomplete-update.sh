#!/bin/sh
# 25 — Incomplete update: kill the VM mid-activation (power loss during
# stage/activate). The router must boot to the previous good image, not brick.
# Requires the same signed lab payloads as scenario 24 (LAB_SIGNED_ROOT).
. "$(dirname "$0")/../lib.sh"

DIST="$(dirname "$0")/../../../build/minimalrouter-linux-amd64.tar.gz"
SIGNED_ROOT="${LAB_SIGNED_ROOT:-/tmp/lab24sig}"
UP_VERSION="${LAB_UPDATE_VERSION:-9.9.9}"

begin "25-incomplete-update"
phase "3-fault"
require "fault: none (update path)" ispfault status

phase "4-mr-runtime"
check "MR up before update" mr "uptime -s | grep -q ."
mr "router-update status 2>/dev/null" > /tmp/lab-pre-version.txt
check "pre-update version recorded" test -s /tmp/lab-pre-version.txt

phase "4.5-operator"
require "dist tarball exists on runner" test -f "$DIST"
require "signed payload prepared on runner" test -f "$SIGNED_ROOT/lab-update-${UP_VERSION}.manifest.json"
mkdir -p /tmp/lab-update-payload
rm -rf /tmp/lab-update-payload/*
tar xzf "$DIST" -C /tmp/lab-update-payload 2>/dev/null
cp "$SIGNED_ROOT/lab-update-${UP_VERSION}.manifest.json" /tmp/lab-update-payload/minimalrouter-linux-amd64/manifest.json
cp "$SIGNED_ROOT/release.pub" /tmp/lab-update-payload/minimalrouter-linux-amd64/firmware-signing.pub 2>/dev/null || true
tar czf /tmp/lab-update.tgz -C /tmp/lab-update-payload minimalrouter-linux-amd64
require "update payload prepared on runner" test -s /tmp/lab-update.tgz
require "push payload to router" mr_put /tmp/lab-update.tgz /root/lab-update.tgz
# chmod: MR-TEST root umask 077 would leave the payload 0700 and the staged
# slot binaries unexecutable by routerd (unprivileged) → bootstrap fallback.
require "router-update stage" mr "mkdir -p /root/lab-update && cd /root/lab-update && rm -rf minimalrouter-linux-amd64 && tar xzf /root/lab-update.tgz && chmod -R 0755 minimalrouter-linux-amd64 && cd minimalrouter-linux-amd64 && router-update stage --dir . --manifest manifest.json 2>&1 | grep -E 'verified and staged|already staged'"

phase "4-mr-runtime-2"
mr "router-update activate --version $UP_VERSION --confirm ACTIVATE-UPDATE & sleep 3; poweroff -f" >/dev/null 2>&1 || true
require "VM actually halted mid-activate" wait_vm_stopped 151 120

phase "4-mr-runtime-3"
require "cold boot MR-TEST" H "qm start 151"
require "MR responds after cold boot" mr_wait 300
require "PPPoE reconnects" wait_pppoe 180

phase "4-mr-runtime-4"
check "LAN up after interrupted update" check_lan_up
check "internet works after interrupted update" check_lan_internet
check "firewall policy-drop after interrupted update" check_fw_not_fail_open
check "router not bricked — API answers" retry 90 mr "curl -sk --max-time 5 -o /dev/null -w '%{http_code}' https://192.168.1.1:8443/api/v1/auth/session | grep -qE '401|403|200'"

phase "7-recovery"
check "canonical + last-good converge" retry 90 check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
