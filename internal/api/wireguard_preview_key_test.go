package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestWireGuardPreviewReportsCanonicalKeyPresenceWithoutDisclosingSecrets(t *testing.T) {
	privateKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	for _, configured := range []bool{false, true} {
		for _, scenario := range []string{"WAN disabled and no domain", "provisioning prerequisites present", "invalid subnet", "exhausted pool"} {
			t.Run(fmt.Sprintf("configured=%v/%s", configured, scenario), func(t *testing.T) {
				cfg := config.DefaultConfig()
				cfg.WAN.Enabled = false
				cfg.Cloudflare.Domain = ""
				cfg.WireGuard.Enabled = false
				cfg.WireGuard.Address = "10.8.0.1/24"
				cfg.WireGuard.PrivateKey = ""
				if configured {
					cfg.WireGuard.PrivateKey = privateKey
				}
				wantStatus := http.StatusOK
				switch scenario {
				case "provisioning prerequisites present":
					cfg.WAN.Enabled = true
					cfg.Cloudflare.Domain = "router.example.com"
				case "invalid subnet":
					cfg.WireGuard.Address = "invalid"
					wantStatus = http.StatusUnprocessableEntity
				case "exhausted pool":
					cfg.WireGuard.Address = "10.8.0.1/30"
					cfg.WireGuard.Peers = []config.WireGuardPeer{{AllowedIPs: []string{"10.8.0.2/32"}}}
					wantStatus = http.StatusConflict
				}
				s := &Server{engine: apply.NewEngine(cfg, nil)}
				before := s.engine.GetCurrentConfig()
				w := httptest.NewRecorder()
				s.handleWireGuardProvisioningPreview(w, httptest.NewRequest(http.MethodGet, "/api/v1/wireguard/provisioning-preview", nil))
				if w.Code != wantStatus {
					t.Fatalf("preview status=%d want=%d", w.Code, wantStatus)
				}
				if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("preview headers=%v", w.Header())
				}
				var preview map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
					t.Fatal(err)
				}
				got, exists := preview["server_key_configured"].(bool)
				if !exists || got != configured {
					t.Fatalf("key presence=%v, must include authoritative boolean %v", preview["server_key_configured"], configured)
				}
				if preview["wireguard_enabled"] != false || preview["ddns_configured"] != (cfg.Cloudflare.Domain != "") {
					t.Fatal("preview lost existing state fields")
				}
				if wantStatus == http.StatusOK {
					if preview["client_ip"] != "10.8.0.2" {
						t.Fatal("preview lost address allocation")
					}
				} else if message, ok := preview["error"].(string); !ok || message == "" {
					t.Fatal("blocking preview lost its error")
				}
				if strings.Contains(w.Body.String(), privateKey) || strings.Contains(w.Body.String(), "private_key") {
					t.Fatal("preview disclosed server key")
				}
				// Both canonical states deliberately have the same redacted GET
				// representation. Clients must use the preview boolean instead.
				public := httptest.NewRecorder()
				s.handleGetConfig(public, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
				var visible config.SystemConfig
				if err := json.Unmarshal(public.Body.Bytes(), &visible); err != nil {
					t.Fatal(err)
				}
				if visible.WireGuard.PrivateKey != redactedSecret || strings.Contains(public.Body.String(), privateKey) {
					t.Fatal("GET config changed unconditional key redaction")
				}
				if !reflect.DeepEqual(before, s.engine.GetCurrentConfig()) {
					t.Fatal("read-only preview/config changed canonical state")
				}
			})
		}
	}
}
