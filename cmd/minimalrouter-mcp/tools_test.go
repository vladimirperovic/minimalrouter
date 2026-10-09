package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
	for _, want := range []string{"get_dns_activity", "get_recent_dns_lookups", "get_security_events", "get_firewall_activity", "get_traffic_insights", "get_health", "get_startup_boots", "get_startup_boot", "get_recovery_status", "list_snapshots", "preview_snapshot_restore", "get_pending_transaction", "get_dns_filter_status", "get_diagnostics"} {
		if !names[want] {
			t.Fatalf("read-only mode is missing %s", want)
		}
	}
}

func TestRecoveryToolsReadContextAndRequireReviewedRevisionForRollback(t *testing.T) {
	var requests []string
	var revision float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		if r.Method == "POST" {
			var payload map[string]float64
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			revision = payload["expected_revision"]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"awaiting_confirmation"}`))
	}))
	defer server.Close()
	oldURL, oldClient, oldMode := routerAPIURL, routerClient, allowMutations
	defer func() { routerAPIURL, routerClient, allowMutations = oldURL, oldClient, oldMode }()
	routerAPIURL = server.URL
	routerClient = &apiClient{http: server.Client()}
	allowMutations = false
	paths := map[string]string{"get_recovery_status": "/api/v1/recovery/status", "list_snapshots": "/api/v1/snapshots", "preview_snapshot_restore": "/api/v1/snapshots/named%20snapshot/preview", "get_pending_transaction": "/api/v1/transactions/pending", "get_dns_filter_status": "/api/v1/dns-filter", "get_diagnostics": "/api/v1/system/diagnostics"}
	for name, path := range paths {
		result, err := executeToolCall(name, map[string]interface{}{"snapshot_id": "named snapshot"})
		if err != nil || !strings.Contains(result, "awaiting_confirmation") {
			t.Fatalf("%s: %s %v", name, result, err)
		}
		if requests[len(requests)-1] != "GET "+path {
			t.Fatalf("bad route: %v", requests)
		}
	}
	if _, err := executeToolCall("rollback_snapshot", map[string]interface{}{"snapshot_id": "named snapshot", "expected_revision": float64(42)}); err == nil {
		t.Fatal("read-only rollback permitted")
	}
	allowMutations = true
	for _, value := range []interface{}{nil, "42", -1.0, 1.5} {
		if _, err := executeToolCall("rollback_snapshot", map[string]interface{}{"snapshot_id": "named snapshot", "expected_revision": value}); err == nil {
			t.Fatalf("invalid revision accepted: %v", value)
		}
	}
	if len(requests) != 6 {
		t.Fatal("rejected mutation reached API")
	}
	if _, err := executeToolCall("rollback_snapshot", map[string]interface{}{"snapshot_id": "named snapshot", "expected_revision": float64(42)}); err != nil {
		t.Fatal(err)
	}
	if revision != 42 || requests[6] != "POST /api/v1/snapshots/named%20snapshot/restore" {
		t.Fatalf("revision not forwarded: %v %v", revision, requests)
	}
}

func TestLogsToolsForwardReadOnlyQueriesAndSelectedBoot(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected mutation: %s", r.Method)
		}
		requests = append(requests, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"has_more":true,"next_cursor":"opaque","status":"timeout"}`))
	}))
	defer server.Close()
	oldURL, oldClient, oldMode := routerAPIURL, routerClient, allowMutations
	defer func() { routerAPIURL, routerClient, allowMutations = oldURL, oldClient, oldMode }()
	routerAPIURL = server.URL
	routerClient = &apiClient{http: server.Client()}
	allowMutations = false
	result, err := executeToolCall("get_security_events", map[string]interface{}{"limit": float64(42), "search": "test&value", "category": "recovery", "cursor": "opaque+cursor", "since": "2026-10-09T00:00:00Z"})
	if err != nil || !strings.Contains(result, `"has_more":true`) {
		t.Fatalf("%s %v", result, err)
	}
	u, _ := url.Parse(requests[0])
	if u.Query().Get("q") != "test&value" || u.Query().Get("cursor") != "opaque+cursor" || u.Query().Get("limit") != "42" {
		t.Fatalf("bad query: %s", requests[0])
	}
	if _, err := executeToolCall("get_startup_boots", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := executeToolCall("get_startup_boot", map[string]interface{}{"boot_id": "test-boot"}); err != nil {
		t.Fatal(err)
	}
	if requests[1] != "/api/v1/startup/boots" || requests[2] != "/api/v1/startup/boots/test-boot" {
		t.Fatalf("bad startup paths: %v", requests)
	}
	for _, value := range []interface{}{float64(0), float64(501), float64(2.5), "10"} {
		if _, err := executeToolCall("get_security_events", map[string]interface{}{"limit": value}); err == nil {
			t.Fatalf("accepted limit %v", value)
		}
	}
	if _, err := executeToolCall("get_startup_boot", map[string]interface{}{"boot_id": "../private"}); err == nil {
		t.Fatal("accepted boot path")
	}
	if len(requests) != 3 {
		t.Fatal("invalid arguments reached API")
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
