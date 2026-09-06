package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestPreviewRestoresStoredSecretsBeforeValidatingEnabledServices(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	current := config.DefaultConfig()
	current.WAN.Enabled, current.WAN.Username, current.WAN.Password = true, "test-user", "stored-pppoe-secret"
	current.WireGuard.Enabled, current.WireGuard.PrivateKey = true, key
	current.WireGuard.Peers = []config.WireGuardPeer{{ID: "peer-one", Name: "Test", PublicKey: key, PresharedKey: key, AllowedIPs: []string{"10.8.0.2/32"}, Enabled: true}}
	current.SquidProxy.Enabled, current.SquidProxy.Password = true, "stored-proxy-secret"
	if err := current.Validate(); err != nil {
		t.Fatal(err)
	}
	server := NewServer(apply.NewEngineWithClient(current, nil, apiTestApplyClient{}))
	candidate := redactConfig(server.engine.GetCurrentConfig())
	candidate.DHCP.StaticLeases = append(candidate.DHCP.StaticLeases, config.StaticLease{ID: "new-lease", MAC: "02:00:00:00:00:20", IPAddress: "192.168.1.20", Hostname: "test-device"})
	payload, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/preview", bytes.NewReader(payload))
	req.RemoteAddr = "192.168.1.50:1234"
	response := httptest.NewRecorder()
	server.handleConfigPreview(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("preview rejected retained secrets: %d %s", response.Code, response.Body.String())
	}
	var preview apply.ChangePreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.RequiresConfirmation || !reflect.DeepEqual(preview.Changes, []string{"DHCP / DNS"}) {
		t.Fatalf("unrelated stored secrets changed preview: %+v", preview)
	}
	for _, secret := range []string{current.WAN.Password, current.SquidProxy.Password, key} {
		if bytes.Contains(response.Body.Bytes(), []byte(secret)) {
			t.Fatal("preview exposed a secret")
		}
	}
	if got := server.engine.GetCurrentConfig(); got.Revision != current.Revision || len(got.DHCP.StaticLeases) != 0 || got.WireGuard.Peers[0].PresharedKey != key {
		t.Fatal("preview changed canonical configuration")
	}
}

func TestRestoreRedactedConfigPreservesExplicitSecretEditsAndPeerIdentity(t *testing.T) {
	current := config.DefaultConfig()
	current.WAN.Password = "existing-wan"
	current.WireGuard.Peers = []config.WireGuardPeer{{ID: "first", PresharedKey: "first-secret"}, {ID: "second", PresharedKey: "second-secret"}}
	candidate := redactConfig(current)
	candidate.WAN.Password = "new-wan"
	candidate.WireGuard.Peers[0], candidate.WireGuard.Peers[1] = candidate.WireGuard.Peers[1], candidate.WireGuard.Peers[0]
	candidate.WireGuard.Peers[1].PresharedKey = ""
	restored := restoreRedactedConfig(candidate, current)
	if restored.WAN.Password != "new-wan" || restored.WireGuard.Peers[0].PresharedKey != "second-secret" || restored.WireGuard.Peers[1].PresharedKey != "" {
		t.Fatal("explicit edit/removal or peer identity lost")
	}
	if candidate.WireGuard.Peers[0].PresharedKey != redactedSecret || current.WireGuard.Peers[0].PresharedKey != "first-secret" {
		t.Fatal("restoration mutated input")
	}
}

func TestConfigPreviewRejectsManagementLockoutBeforeApply(t *testing.T) {
	current := config.DefaultConfig()
	server := NewServer(apply.NewEngineWithClient(current, nil, apiTestApplyClient{}))
	candidate := redactConfig(current)
	candidate.TrustedNetworks = []string{"192.168.2.0/24"}
	payload, _ := json.Marshal(candidate)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/preview", bytes.NewReader(payload))
	req.RemoteAddr = "192.168.1.50:1234"
	response := httptest.NewRecorder()
	server.handleConfigPreview(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("lockout preview returned %d: %s", response.Code, response.Body.String())
	}
}
