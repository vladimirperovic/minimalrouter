package services

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// A semantic identity prevents counters for an edited rule being attributed to
// its old match. Names and preset provenance do not affect packet matching.
func FirewallRuleCounter(rule config.FirewallRule) string {
	rule.Name = ""
	rule.PresetID = ""
	rule.Scope = config.FirewallRuleScope(rule)
	b, _ := json.Marshal(rule)
	sum := sha256.Sum256(b)
	return fmt.Sprintf("mr_rule_%x", sum[:12])
}
