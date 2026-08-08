#!/bin/sh
# 23 — Power loss at each fault-hook phase: kill the VM with `qm stop` at
# phase pre-privileged-apply / post-provisional-apply / pre-sqlite-commit /
# post-sqlite-commit / pre-canonical-ack, then boot and verify the router
# converges to a consistent, policy-drop state.
#
# NOTE: during-final-reconcile is excluded: that hook fires only on
# OpReconcile (LAN-change finalize or boot reconciliation), never on the
# routine save path this scenario exercises.

. "$(dirname "$0")/../lib.sh"

for PHASE in pre-privileged-apply post-provisional-apply pre-sqlite-commit post-sqlite-commit pre-canonical-ack; do
  begin "23-power-loss-$PHASE"

  phase "3-fault"
  require "arm hook: halt on phase $PHASE" arm_hook "$PHASE" 'if [ $(id -u) -eq 0 ]; then poweroff -f; else doas /sbin/poweroff -f; fi'

  phase "4-mr-runtime"
  check "MR up before fault" mr "uptime -s | grep -q ."

  phase "4.5-operator"
  save_config >/dev/null 2>&1 || true
  require "VM actually halted (power cut at $PHASE)" wait_vm_stopped 108 120

  phase "4-mr-runtime-2"
  require "cold boot MR-TEST" H "qm start 108"
  require "MR responds after cold boot" mr_wait 300
  require "PPPoE reconnects after power loss" wait_pppoe 180

  phase "5-lan-client"
  check "LAN up after power loss" check_lan_up
  check "local DNS serves after power loss" check_local_dns
  check "client internet after power loss" check_lan_internet
  check "firewall NOT fail-open after power loss" check_fw_not_fail_open

  phase "7-recovery"
  check "canonical + last-good converge after power loss" check_converge
  check "runtime not hybrid after power loss" check_runtime_not_hybrid
  check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

  capture_state "evidence"
  [ "$FAILED" -eq 0 ] && echo "[RESULT] 23-power-loss-$PHASE: PASS" || echo "[RESULT] 23-power-loss-$PHASE: FAIL ($FAILED failed checks)"
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) rc=$([ "$FAILED" -eq 0 ] && echo 0 || echo 1)" > "$RESULTS_DIR/23-power-loss-$PHASE/result.txt"

  phase "3-disarm"
  require "disarm hooks for next phase" disarm_hooks
done

finish_scenario
