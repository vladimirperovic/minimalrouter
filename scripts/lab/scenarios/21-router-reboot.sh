#!/bin/sh
# 21 — Router reboot: full cold restart of MR-TEST. After boot: LAN, DHCP,
# DNS, PPPoE and internet must all come back without operator action.
. "$(dirname "$0")/../lib.sh"

begin "21-router-reboot"
phase "3-fault"
require "fault: none (operator-triggered reboot)" ispfault status

phase "4-mr-runtime"
check "MR up before reboot" mr "uptime -s | grep -q ."

phase "4.5-operator"
require "reboot MR-TEST VM" H "qm reboot $MR_VMID --timeout 30 && qm wait $MR_VMID --timeout 90"
require "MR responds after reboot" mr_wait 240

phase "4-mr-runtime-2"
require "PPPoE reconnects after reboot" wait_pppoe 180
check "LAN up after reboot" check_lan_up
check "local DNS serves after reboot" check_local_dns
check "firewall policy-drop after reboot" check_fw_not_fail_open

phase "5-lan-client"
check "client renews lease after router reboot" lan_dhcp_renew
check "client internet back after reboot" check_lan_internet

phase "7-recovery"
check "canonical + last-good converge after reboot" check_converge
check "runtime not hybrid after reboot" check_runtime_not_hybrid
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
