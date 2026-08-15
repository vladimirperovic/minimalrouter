#!/bin/sh
# 23 — Power loss at each fault-hook phase: force an immediate guest poweroff
# from the exact hook point at pre-privileged-apply / post-provisional-apply /
# pre-sqlite-commit / post-sqlite-commit / pre-canonical-ack, then boot and
# verify the router converges to a consistent, policy-drop state.
#
# NOTE: during-final-reconcile is excluded: that hook fires only on
# OpReconcile (LAN-change finalize or boot reconciliation), never on the
# routine save path this scenario exercises.

. "$(dirname "$0")/../lib.sh"

TOTAL_FAILED=0
for PHASE in pre-privileged-apply post-provisional-apply pre-sqlite-commit post-sqlite-commit pre-canonical-ack; do
  begin "23-power-loss-$PHASE"

  phase "3-fault"
  require "arm hook: halt on phase $PHASE" arm_hook "$PHASE" 'if [ $(id -u) -eq 0 ]; then poweroff -f; else doas /sbin/poweroff -f; fi'

  phase "4-mr-runtime"
  check "MR up before fault" mr "uptime -s | grep -q ."

  phase "4.5-operator"
  save_config >/dev/null 2>&1 || true
  require "VM actually halted (power cut at $PHASE)" wait_vm_stopped "$MR_VMID" 120

  phase "4-mr-runtime-2"
  require "cold boot MR-TEST" H "qm start $MR_VMID"
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

  # Hook cleanup is part of the phase result. It must happen before a PASS is
  # recorded, and failure must abort the campaign so the next iteration can
  # never run with a stale fault armed.
  phase "3-disarm"
  require "disarm hooks for next phase" disarm_hooks

  capture_state "evidence"
  [ "$FAILED" -eq 0 ] && echo "[RESULT] 23-power-loss-$PHASE: PASS" || echo "[RESULT] 23-power-loss-$PHASE: FAIL ($FAILED failed checks)"
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) rc=$([ "$FAILED" -eq 0 ] && echo 0 || echo 1)" > "$RESULTS_DIR/23-power-loss-$PHASE/result.txt"
  TOTAL_FAILED=$((TOTAL_FAILED+FAILED))
done

FAILED="$TOTAL_FAILED"
CURRENT_SCENARIO="23-power-loss-hooks"
mkdir -p "$RESULTS_DIR/$CURRENT_SCENARIO"
finish_scenario
