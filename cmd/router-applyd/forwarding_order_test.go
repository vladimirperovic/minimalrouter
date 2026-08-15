package main

import (
	"os"
	"strings"
	"testing"
)

// Routing must never be switched on while the generated nftables policy is
// absent. That is an ordering invariant spanning several functions, and it can
// only be observed for real against a live kernel as root, so it is asserted
// here against the source that encodes it. The lab covers the runtime side in
// scenario 152.

func applydSource(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// functionBody returns the source of the function starting with signature, up
// to the next top-level declaration.
func functionBody(t *testing.T, source, signature string) string {
	t.Helper()
	start := strings.Index(source, signature)
	if start < 0 {
		t.Fatalf("function %q no longer exists", signature)
	}
	rest := source[start+len(signature):]
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		return rest[:end]
	}
	return rest
}

func TestForwardingIsEnabledOnlyAfterTheGeneratedPolicyLoads(t *testing.T) {
	source := applydSource(t, "main.go")

	// applyKernelHardening runs before the ruleset is loaded, so it must not be
	// the thing that turns routing on.
	if strings.Contains(functionBody(t, source, "func applyKernelHardening("), "net.ipv4.ip_forward") {
		t.Fatal("applyKernelHardening enables forwarding; it runs before nftables is loaded, which opens the WAN to LAN with the kernel default ACCEPT policy")
	}

	apply := functionBody(t, source, "func installAndActivate(")
	policy := strings.Index(apply, "runNftFile(nftRuntimePath, false)")
	forwarding := strings.Index(apply, "enableIPForwarding()")
	if policy < 0 || forwarding < 0 {
		t.Fatal("installAndActivate no longer loads the ruleset and enables forwarding as separate, ordered steps")
	}
	if forwarding < policy {
		t.Fatal("installAndActivate enables forwarding before the nftables policy is loaded")
	}
}

func TestRollbackWithoutAPreviousPolicyTurnsRoutingOff(t *testing.T) {
	body := functionBody(t, applydSource(t, "main.go"), "func rollback(")

	remove := strings.Index(body, `"delete", "table", "inet", "minimalrouter"`)
	if remove < 0 {
		t.Fatal("rollback no longer removes the candidate table when there is no previous ruleset")
	}
	disable := strings.Index(body, "disableIPForwarding()")
	if disable < 0 || disable > remove {
		t.Fatal("rollback deletes the only nftables policy while leaving ip_forward=1, which is the same unprotected window reached through the failure path")
	}
}
