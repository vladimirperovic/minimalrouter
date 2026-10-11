package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/health"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

type firewallObservation struct {
	Counters     *apply.FirewallCounters
	LastIncrease map[string]time.Time
}

var firewallObservations sync.Map

// PublishFirewallObservation is fed by the existing minute collector; HTTP reads
// never launch privileged commands or race apply operations on the UI's behalf.
func (s *Server) PublishFirewallObservation(value *apply.FirewallCounters) {
	if value == nil {
		firewallObservations.Delete(s)
		return
	}
	increases := map[string]time.Time{}
	if old, ok := firewallObservations.Load(s); ok {
		previous := old.(firewallObservation)
		if previous.Counters.Generation == value.Generation {
			for key, n := range value.RulePackets {
				if before, exists := previous.Counters.RulePackets[key]; exists && n >= before {
					if n > before {
						increases[key] = value.ObservedAt
					} else if at, ok := previous.LastIncrease[key]; ok {
						increases[key] = at
					}
				}
			}
		}
	}
	firewallObservations.Store(s, firewallObservation{Counters: value, LastIncrease: increases})
}

type firewallRuntime struct {
	Available          bool              `json:"available"`
	ObservedAt         *time.Time        `json:"observed_at"`
	DefaultDeny        bool              `json:"default_deny"`
	Policies           map[string]string `json:"policies"`
	LoadedRevision     *config.Revision  `json:"loaded_revision"`
	RevisionMatches    bool              `json:"revision_matches"`
	ConfigurationMatch string            `json:"configuration_match"`
	Generation         string            `json:"generation,omitempty"`
}

func (s *Server) firewallRuntime(revision config.Revision) (firewallRuntime, firewallObservation) {
	r := firewallRuntime{ConfigurationMatch: "unverified", Policies: map[string]string{}}
	stored, ok := firewallObservations.Load(s)
	if !ok {
		return r, firewallObservation{}
	}
	obs := stored.(firewallObservation)
	c := obs.Counters
	r.ObservedAt = &c.ObservedAt
	age := time.Since(c.ObservedAt)
	r.Available = age >= 0 && age <= 180*time.Second
	r.Policies = c.Policies
	r.LoadedRevision = c.LoadedRevision
	r.Generation = c.Generation
	r.RevisionMatches = r.Available && c.LoadedRevision != nil && *c.LoadedRevision == revision
	r.DefaultDeny = r.Available && c.Policies["input"] == "drop" && c.Policies["forward"] == "drop" && c.Policies["output"] == "drop"
	return r, obs
}

func (s *Server) firewallHealth(revision config.Revision) health.Check {
	r, _ := s.firewallRuntime(revision)
	check := health.Check{ID: "firewall", Label: "Firewall base policy", State: health.StateUnknown, Summary: "Fresh firewall observation unavailable; configured policy is not runtime evidence."}
	if r.Available {
		check.State = health.StateDegraded
		check.Summary = "Expected default-drop base chains were not observed."
		if r.DefaultDeny {
			check.State = health.StateHealthy
			check.Summary = "Input, forward and output base policies are drop. Full ruleset equivalence is not verified."
		}
		if r.DefaultDeny && !r.RevisionMatches {
			check.State = health.StateWarning
			check.Summary = "Default-drop base policies observed; current configuration revision marker is missing or differs."
		}
	}
	return check
}

func (s *Server) handleFirewallStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.GetCurrentConfig()
	runtime, obs := s.firewallRuntime(cfg.Revision)
	var pending any
	if tx := s.engine.GetPendingTransaction(); tx != nil {
		pending = map[string]any{"id": tx.ID, "state": tx.CurrentState, "confirmation_deadline": tx.ConfirmationDeadline}
	}
	type ruleStatus struct {
		ID           string     `json:"id"`
		Name         string     `json:"name"`
		Scope        string     `json:"scope"`
		Packets      *uint64    `json:"packets"`
		LastIncrease *time.Time `json:"last_increase_observed_at"`
	}
	rules := []ruleStatus{}
	for _, rule := range cfg.Firewall.CustomRules {
		item := ruleStatus{ID: rule.ID, Name: rule.Name, Scope: config.FirewallRuleScope(rule)}
		if runtime.Available && runtime.RevisionMatches && rule.Enabled && obs.Counters != nil {
			key := services.FirewallRuleCounter(rule)
			if n, ok := obs.Counters.RulePackets[key]; ok {
				item.Packets = &n
			}
			if at, ok := obs.LastIncrease[key]; ok {
				item.LastIncrease = &at
			}
		}
		rules = append(rules, item)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"revision": cfg.Revision, "runtime": runtime, "rules": rules, "engine": s.engine.GetStatus(), "pending_transaction": pending,
		"warnings":    services.FirewallWarnings(cfg),
		"limitations": []string{"Base policies and revision markers do not prove equivalence of all live rules or end-to-end reachability.", "Rule counts are since the current table generation, sampled every minute; last increase is an observation time, not a packet timestamp."},
	})
}

func (s *Server) handleFirewallExplain(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil {
		http.Error(w, "port must be an integer", 400)
		return
	}
	result, err := services.ExplainFirewallFlow(s.engine.GetCurrentConfig(), services.FirewallFlow{SourceIP: r.URL.Query().Get("source_ip"), DestinationIP: r.URL.Query().Get("destination_ip"), Protocol: r.URL.Query().Get("protocol"), Port: port})
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
