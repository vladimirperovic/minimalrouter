#!/bin/sh
# 23 — Power loss at each fault-hook phase: kill the VM with `qm stop` at
# phase pre-privileged-apply / post-provisional-apply / pre-sqlite-commit /
# post-sqlite-commit / pre-canonical-ack, then boot and verify the router
# converges to a consistent, policy-drop state.
#
# Per the faultinject design (internal/faultinject/hook.go) the hook command
# BLOCKS at the transaction phase, giving this runner a deterministic window
# to hard-stop the VM with `qm stop` from the Proxmox host. The hook writes a
# marker file (world-writable /tmp) then sleeps; the runner polls for the
# marker and issues the power cut. No guest-side privilege escalation is used:
# routerd runs unprivileged with NoNewPrivs=1, so setuid doas can never work.
#
# NOTE: during-final-reconcile is excluded: that hook fires only on
# OpReconcile (LAN-change finalize or boot reconciliation), never on the
# routine save path this scenario exercises.

. "$(dirname "$0")/../lib.sh"

TOTAL_FAILED=0
for PHASE in pre-privileged-apply post-provisional-apply pre-sqlite-commit post-sqlite-commit pre-canonical-ack; do
  begin "23-power-loss-$PHASE"
  MARKER="/tmp/mr-fault-$PHASE"

  phase "3-fault"
  require "arm blocking hook on phase $PHASE" arm_hook "$PHASE" "touch $MARKER && sleep 300"

  phase "4-mr-runtime"
  check "MR up before fault" mr "uptime -s | grep -q ."

  phase "4.5-operator"
  mr "rm -f $MARKER" >/dev/null 2>&1
  save_config >/dev/null 2>&1 &
  SC_PID=$!
  fired=""
  i=0
  while [ $i -lt 120 ]; do
    if mr "test -f $MARKER" >/dev/null 2>&1; then fired=1; break; fi
    sleep 1; i=$((i+1))
  done
  kill "$SC_PID" 2>/dev/null; wait "$SC_PID" 2>/dev/null
  require "hook reached phase $PHASE" test -n "$fired"
  require "power cut (qm stop) at $PHASE" H "qm stop $MR_VMID"
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

  capture_state "evidence"
  [ "$FAILED" -eq 0 ] && echo "[RESULT] 23-power-loss-$PHASE: PASS" || echo "[RESULT] 23-power-loss-$PHASE: FAIL ($FAILED failed checks)"
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) rc=$([ "$FAILED" -eq 0 ] && echo 0 || echo 1)" > "$RESULTS_DIR/23-power-loss-$PHASE/result.txt"
  TOTAL_FAILED=$((TOTAL_FAILED+FAILED))

  phase "3-disarm"
  require "disarm hooks for next phase" disarm_hooks
done

FAILED="$TOTAL_FAILED"
CURRENT_SCENARIO="23-power-loss-hooks"
mkdir -p "$RESULTS_DIR/$CURRENT_SCENARIO"
finish_scenario
