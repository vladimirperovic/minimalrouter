package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDNSInsightToolsAreReadOnlyAndForwardDeviceContext(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			t.Errorf("mutation: %s", r.Method)
		}
		if r.URL.Path == "/api/v1/dns-filter/check" && (r.URL.Query().Get("domain") != "cdn.example.com" || r.URL.Query().Get("device_ip") != "192.0.2.5") {
			t.Errorf("lost context: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"scope":"configured_policy"}`))
	}))
	defer server.Close()
	previousURL, previousClient, previousMode := routerAPIURL, routerClient, allowMutations
	defer func() { routerAPIURL, routerClient, allowMutations = previousURL, previousClient, previousMode }()
	routerAPIURL, routerClient, allowMutations = server.URL, &apiClient{http: server.Client()}, false
	advertised := map[string]bool{}
	for _, tool := range getToolList() {
		advertised[tool.Name] = true
	}
	for _, name := range []string{"get_dns_filter_profiles", "get_dns_filter_operations", "check_dns_filter_domain"} {
		if !advertised[name] || isMutationTool(name) {
			t.Fatalf("read-only tool missing: %s", name)
		}
		if _, err := executeToolCall(name, map[string]interface{}{"domain": "cdn.example.com", "device_ip": "192.0.2.5"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range []map[string]interface{}{{}, {"domain": "https://example.com"}, {"domain": "example.com", "device_ip": "::1"}, {"domain": "example.com", "device_ip": 7}} {
		if _, err := executeToolCall("check_dns_filter_domain", args); err == nil {
			t.Fatalf("invalid input accepted: %v", args)
		}
	}
	if calls != 3 {
		t.Fatalf("rejected requests reached API: %d", calls)
	}
}
