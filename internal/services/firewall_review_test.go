package services

import (
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// A restore preview of a fresh export of the running configuration listed
// "Isolated-segment access policy changes" next to "No effective configuration
// changes detected": the candidate went through DeepCopy, which turned a nil
// extra-LAN list into an empty one.
func TestDescribeFirewallChangesIgnoresUnchangedCopy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Firewall.ExtraLANs = nil
	if changes := DescribeFirewallChanges(cfg.Firewall, cfg.DeepCopy().Firewall); len(changes) != 0 {
		t.Fatalf("an unchanged configuration produced firewall changes: %v", changes)
	}
}
