#!/bin/sh
# 24 — Signed update + rollback: stage a freshly-signed image on the router,
# activate it, verify it runs, then roll back to the previous build.
#
# The payload is signed with a lab release key (firmware-keygen / firmware-sign
# on the runner) whose public half must be installed as
# /etc/minimalrouter/firmware-signing.pub on MR-TEST — that trust anchor is
# what the updater verifies before staging. See ci-fresh-install-smoke.sh for
# the canonical signing flow.
. "$(dirname "$0")/../lib.sh"

DIST="$(dirname "$0")/../../../build/minimalrouter-linux-amd64.tar.gz"
SIGNED_ROOT="${LAB_SIGNED_ROOT:-/tmp/lab24sig}"
UP_VERSION="${LAB_UPDATE_VERSION:-9.9.9}"
BASE_VERSION="${LAB_UPDATE_BASE_VERSION:-9.9.8}"

begin "24-signed-update"
phase "3-fault"
require "fault: none (update path)" ispfault status

phase "4-mr-runtime"
check "MR up before update" mr "uptime -s | grep -q ."
api_login
require "pre-update config readable" api GET /api/v1/config
mr "router-update status 2>/dev/null" > /tmp/lab-pre-version.txt
check "pre-update version recorded" test -s /tmp/lab-pre-version.txt

# prepare_payload <version> <manifest-suffix> <out-tgz>
prepare_payload() {
  ver="$1"; mf="$2"; out="$3"
  rm -rf /tmp/lab-update-payload/* 
  tar xzf "$DIST" -C /tmp/lab-update-payload 2>/dev/null
  cp "$SIGNED_ROOT/$mf" /tmp/lab-update-payload/minimalrouter-linux-amd64/manifest.json
  cp "$SIGNED_ROOT/release.pub" /tmp/lab-update-payload/minimalrouter-linux-amd64/firmware-signing.pub 2>/dev/null || true
  tar czf "$out" -C /tmp/lab-update-payload minimalrouter-linux-amd64
}

phase "4.5-operator"
require "dist tarball exists on runner" test -f "$DIST"
require "baseline manifest prepared on runner" test -f "$SIGNED_ROOT/lab-update-${BASE_VERSION}.manifest.json"
require "update manifest prepared on runner" test -f "$SIGNED_ROOT/lab-update-${UP_VERSION}.manifest.json"
mkdir -p /tmp/lab-update-payload
prepare_payload "$BASE_VERSION" "lab-update-${BASE_VERSION}.manifest.json" /tmp/lab-update-base.tgz
require "baseline payload prepared on runner" test -s /tmp/lab-update-base.tgz
prepare_payload "$UP_VERSION" "lab-update-${UP_VERSION}.manifest.json" /tmp/lab-update.tgz
require "update payload prepared on runner" test -s /tmp/lab-update.tgz

# stage_baseline <tgz> — push, unpack and stage; activate only if not current
stage_activate() {
  tgz="$1"; ver="$2"
  require "push payload to router" mr_put "$tgz" /root/lab-update.tgz
  require "router-update stage" mr "mkdir -p /root/lab-update && cd /root/lab-update && tar xzf /root/lab-update.tgz && cd minimalrouter-linux-amd64 && router-update stage --dir . --manifest manifest.json 2>&1 | grep -q staged"
  require "activate staged image" mr "router-update activate --version $ver --confirm ACTIVATE-UPDATE 2>&1 | grep -qi activated"
  require "router reboots into new image" mr_wait 300
  require "PPPoE reconnects" wait_pppoe 180
}

phase "4-mr-runtime-2"
# baseline first: activate 9.9.8 so a previous slot exists for rollback
stage_activate /tmp/lab-update-base.tgz "$BASE_VERSION"
stage_activate /tmp/lab-update.tgz "$UP_VERSION"

phase "4-mr-runtime-3"
require "PPPoE reconnects after update" wait_pppoe 180
check "LAN up after update" check_lan_up
check "internet works after update" check_lan_internet
check "firewall policy-drop after update" check_fw_not_fail_open

phase "4.5-rollback"
require "roll back to previous build" mr "router-update rollback --confirm ROLLBACK-UPDATE 2>&1 | grep -qi restored"
require "router reboots into old image" mr_wait 300

phase "4-mr-runtime-4"
require "PPPoE reconnects after rollback" wait_pppoe 180
check "LAN up after rollback" check_lan_up
check "internet works after rollback" check_lan_internet
mr "router-update status 2>/dev/null" > /tmp/lab-post-rollback-version.txt
check "version matches baseline after rollback" grep -q "$BASE_VERSION" /tmp/lab-post-rollback-version.txt

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
