package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLANToolsAreReadOnlyAndBoundArguments(t *testing.T) {
	calls := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.String())
		w.Write([]byte(`{"available":false}`))
	}))
	defer srv.Close()
	oldURL, oldClient, oldMode := routerAPIURL, routerClient, allowMutations
	defer func() { routerAPIURL, routerClient, allowMutations = oldURL, oldClient, oldMode }()
	routerAPIURL, routerClient, allowMutations = srv.URL, &apiClient{http: srv.Client()}, false
	for _, name := range []string{"get_lan_dhcp_status", "get_network_interfaces", "list_dhcp_leases", "check_ip_availability"} {
		if isMutationTool(name) {
			t.Fatal(name)
		}
		if _, err := executeToolCall(name, map[string]interface{}{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range []map[string]interface{}{{"limit": 0.0}, {"offset": -1.0}, {"limit": 1.5}, {"sort": "invalid"}, {"search": true}} {
		if _, err := executeToolCall("list_dhcp_leases", args); err == nil {
			t.Fatal(args)
		}
	}
	for _, args := range []map[string]interface{}{{"ip": "::1"}, {"mac": "broken"}, {"ip": true}} {
		if _, err := executeToolCall("check_ip_availability", args); err == nil {
			t.Fatal(args)
		}
	}
	if len(calls) != 4 {
		t.Fatal(calls)
	}
	for _, call := range calls {
		if !strings.HasPrefix(call, "GET ") {
			t.Fatal(call)
		}
	}
}

func TestConfigureDNSValidatesOneToThreeAndPreviewsBeforeWrite(t *testing.T) {
	calls := []string{}
	var saved map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == "GET" {
			w.Write([]byte(`{"revision":7,"dhcp":{"dns_servers":["1.1.1.1"]}}`))
			return
		}
		if r.Method == "PUT" {
			json.NewDecoder(r.Body).Decode(&saved)
		}
		w.Write([]byte(`{"id":"test","state":"Committed","config":{"revision":8}}`))
	}))
	defer srv.Close()
	oldURL, oldClient, oldMode := routerAPIURL, routerClient, allowMutations
	defer func() { routerAPIURL, routerClient, allowMutations = oldURL, oldClient, oldMode }()
	routerAPIURL, routerClient, allowMutations = srv.URL, &apiClient{http: srv.Client()}, true
	for _, args := range []map[string]interface{}{{}, {"primary_dns": "not-ip"}, {"primary_dns": "1.1.1.1\n# injected"}, {"primary_dns": "127.0.0.1"}, {"primary_dns": "1.1.1.1", "secondary_dns": "1.1.1.1"}} {
		if _, err := executeToolCall("configure_dns", args); err == nil {
			t.Fatal(args)
		}
	}
	if len(calls) != 0 {
		t.Fatal("invalid arguments reached router", calls)
	}
	for _, args := range []map[string]interface{}{{"primary_dns": "9.9.9.9"}, {"primary_dns": "9.9.9.9", "secondary_dns": "1.1.1.1", "tertiary_dns": "8.8.8.8"}} {
		calls = nil
		if _, err := executeToolCall("configure_dns", args); err != nil {
			t.Fatal(err)
		}
		if strings.Join(calls, ",") != "GET /api/v1/config,POST /api/v1/config/preview,PUT /api/v1/config" {
			t.Fatal(calls)
		}
		if len(saved["dhcp"].(map[string]interface{})["dns_servers"].([]interface{})) != len(args) {
			t.Fatal(saved)
		}
	}
}
