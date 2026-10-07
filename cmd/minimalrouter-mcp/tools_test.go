package main

import (
	"net/url"
	"testing"
)

func TestReadOnlyModeAdvertisesInsightToolsOnly(t *testing.T) {
	previous := allowMutations
	allowMutations = false
	defer func() { allowMutations = previous }()
	names := map[string]bool{}
	for _, tool := range getToolList() {
		names[tool.Name] = true
		if isMutationTool(tool.Name) {
			t.Fatalf("read-only mode advertises mutation tool %s", tool.Name)
		}
	}
	for _, want := range []string{"get_dns_activity", "get_recent_dns_lookups", "get_security_events", "get_firewall_activity", "get_traffic_insights", "get_health"} {
		if !names[want] {
			t.Fatalf("read-only mode is missing %s", want)
		}
	}
}

func TestToolArgumentsBecomeEncodedQueryParameters(t *testing.T) {
	query := url.Values{}
	addStringArg(query, "q", map[string]interface{}{"search": " porn&device=1 "}, "search")
	addNumberArg(query, "limit", map[string]interface{}{"limit": float64(50)}, "limit")
	addNumberArg(query, "skip", map[string]interface{}{"skip": "50"}, "skip")
	if got := query.Encode(); got != "limit=50&q=porn%26device%3D1" {
		t.Fatalf("query = %q", got)
	}
}
