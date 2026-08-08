#!/bin/sh
# 22 — Service crash: kill routerd and router-applyd on the router. They must
# restart (initd respawn), and the router must keep serving traffic.
. "$(dirname "$0")/../lib.sh"

begin "22-service-crash"
phase "3-fault"
require "fault: none (operator-triggered crash)" ispfault status

phase "4-mr-runtime"
require "kill routerd" mr "pkill -9 routerd; sleep 2"
require "kill router-applyd" mr "pkill -9 router-applyd; sleep 2"

phase "4-mr-runtime-2"
require "routerd restarted by initd" retry 60 mr "rc-service routerd status | grep -q started"
require "router-applyd restarted by initd" retry 60 mr "rc-service router-applyd status | grep -q started"

phase "4-mr-runtime-3"
check "API answers after crash-restart" retry 60 mr "curl -sk --max-time 5 -o /dev/null -w '%{http_code}' https://192.168.1.1:8443/api/v1/auth/session | grep -qE '401|403|200'"
check "PPPoE session survives daemon crash" check_pppoe
check "LAN client internet survives daemon crash" check_lan_internet
check "firewall still policy-drop" check_fw_not_fail_open

phase "7-recovery"
check "canonical + last-good converge" retry 90 check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
