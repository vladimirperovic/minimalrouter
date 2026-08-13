#!/bin/sh
# 68 — Log rotation: rotating the router logs frees space and services keep
# logging.
. "$(dirname "$0")/../lib.sh"
begin "68-storage-log-rotation"
phase "3-fault"
require "fault: none (logrotate)" ispfault status
phase "4.5-operator"
require "logrotate completes" mr "logrotate -f /etc/logrotate.conf"
sleep 2
api_login
api GET /api/v1/system >/dev/null 2>&1
phase "4-mr-runtime-2"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "applyd still alive" mr "rc-service router-applyd status | grep -q started"
check "rotated router log retained" mr "find /var/log -maxdepth 1 -name 'routerd.*' -type f | grep -q ."
check "router continues logging after rotation" retry 20 mr "test -s /var/log/routerd.log || test -s /var/log/routerd.err"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
