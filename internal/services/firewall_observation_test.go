package services

import (
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"strings"
	"testing"
)

func TestFirewallObservationDoesNotReplaceDefaultDeny(t *testing.T) {
	cfg := config.DefaultConfig()
	out, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{"input", "forward"} {
		for _, want := range []string{"chain observe_" + hook + "_before { type filter hook " + hook + " priority -1; policy accept; counter name mr_seen; }", "chain observe_" + hook + "_after { type filter hook " + hook + " priority 1; policy accept; counter name mr_accepted; }", "type filter hook " + hook + " priority filter; policy drop;"} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %s", want)
			}
		}
	}
	if strings.Count(out, "counter mr_seen { }") != 1 || strings.Count(out, "counter mr_accepted { }") != 1 {
		t.Fatal("aggregate counters missing")
	}
}
