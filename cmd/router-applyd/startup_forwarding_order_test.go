package main

import (
	"strings"
	"testing"
)

// Cold boot goes through restoreLastGoodRuntime, not installAndActivate. Keep
// the same firewall-before-forwarding invariant asserted on the actual startup
// reconciliation path so a future refactor cannot re-open the boot window while
// the normal apply test remains green.
func TestStartupReconcileLoadsPolicyBeforeForwarding(t *testing.T) {
	source := applydSource(t, "startup_reconcile.go")
	body := functionBody(t, source, "func restoreLastGoodRuntime(")
	policy := strings.Index(body, "runNftFile(nftRuntimePath, false)")
	forwarding := strings.Index(body, "enableIPForwarding()")
	if policy < 0 || forwarding < 0 {
		t.Fatal("startup reconcile no longer has separate firewall load and forwarding steps")
	}
	if forwarding < policy {
		t.Fatal("startup reconcile enables IPv4 forwarding before nftables is loaded")
	}
}

// First-run recovery intentionally exposes only the local setup plane. It must
// never enable routing, even if the shared kernel-hardening helper changes in a
// later release.
func TestFirstRunRecoveryKeepsForwardingDisabled(t *testing.T) {
	source := applydSource(t, "startup_reconcile.go")
	body := functionBody(t, source, "func restoreFirstRunRuntime(")
	if strings.Contains(body, "if err := enableIPForwarding()") {
		t.Fatal("first-run recovery enables forwarding")
	}
	if !strings.Contains(body, "net.ipv4.ip_forward=0") {
		t.Fatal("first-run recovery no longer positively asserts ip_forward=0")
	}
}
