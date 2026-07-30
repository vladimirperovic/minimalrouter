package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestFirstRunKeepsCloudflareAndWiFiDisabled(t *testing.T) {
	server, mux, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	body, err := json.Marshal(map[string]string{
		"wan_interface":  "eth0",
		"pppoe_username": "test-user",
		"pppoe_password": "test-password",
		"admin_password": "longsecureadminpassword123!",
		"lan_interface":  "eth1",
		"lan_ip_address": "192.168.1.1",
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup/apply", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("setup failed: %d %s", response.Code, response.Body.String())
	}

	cfg := server.engine.GetCurrentConfig()
	if cfg.Cloudflare.DDNSEnabled || cfg.Cloudflare.TunnelEnabled {
		t.Fatal("first-run setup enabled a Cloudflare integration")
	}
	if cfg.WiFi.Enabled {
		t.Fatal("first-run setup enabled the Wi-Fi access point")
	}
	if cfg.IoT.Enabled || cfg.Policies.Enabled {
		t.Fatal("first-run setup enabled IoT isolation or device schedules without explicit opt-in")
	}
}
