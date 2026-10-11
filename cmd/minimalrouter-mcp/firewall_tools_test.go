package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFirewallToolsReadOnlyAndUnadvertisedHandlersRejectedEvenInAdmin(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			t.Errorf("unexpected write %s", r.Method)
		}
		if r.URL.Path == "/api/v1/firewall/explain" && (r.URL.Query().Get("source_ip") != "192.168.1.50" || r.URL.Query().Get("port") != "443") {
			t.Errorf("lost flow %s", r.URL)
		}
		w.Write([]byte(`{"basis":"configured_policy"}`))
	}))
	defer server.Close()
	oldURL, oldClient, oldMode := routerAPIURL, routerClient, allowMutations
	defer func() { routerAPIURL, routerClient, allowMutations = oldURL, oldClient, oldMode }()
	routerAPIURL, routerClient, allowMutations = server.URL, &apiClient{http: server.Client()}, false
	for _, name := range []string{"get_firewall_status", "explain_firewall_flow"} {
		if isMutationTool(name) {
			t.Fatal("read marked as write")
		}
		if _, err := executeToolCall(name, map[string]interface{}{"source_ip": "192.168.1.50", "destination_ip": "1.1.1.1", "protocol": "tcp", "port": float64(443)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range []map[string]interface{}{{}, {"source_ip": "::1", "destination_ip": "1.1.1.1", "protocol": "tcp", "port": float64(443)}, {"source_ip": "192.168.1.50", "destination_ip": "1.1.1.1", "protocol": "tcp", "port": 1.5}, {"source_ip": "192.168.1.50", "destination_ip": "1.1.1.1", "protocol": "icmp", "port": float64(443)}} {
		if _, err := executeToolCall("explain_firewall_flow", args); err == nil {
			t.Fatal("invalid accepted", args)
		}
	}
	allowMutations = true
	for _, name := range []string{"add_port_forward", "block_device_ip", "configure_squid", "nonexistent"} {
		if _, err := executeToolCall(name, map[string]interface{}{}); err == nil {
			t.Fatal("unadvertised handler callable", name)
		}
	}
	if calls != 2 {
		t.Fatalf("rejected request reached API: %d", calls)
	}
}
