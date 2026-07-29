package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/services"
)

func TestFreshInstallWizardProducesWorkingRouterBaseline(t *testing.T) {
	server, mux, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	body, err := json.Marshal(map[string]string{
		"wan_interface":  "enp1s0",
		"pppoe_username": "placeholder@isp.example",
		"pppoe_password": "placeholder-pppoe-password",
		"admin_password": "placeholder-admin-password-123!",
		"lan_interface":  "enp2s0",
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
		t.Fatalf("fresh setup failed: %d %s", response.Code, response.Body.String())
	}

	cfg := server.engine.GetCurrentConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("wizard produced invalid canonical configuration: %v", err)
	}
	if !cfg.WAN.Enabled || cfg.WAN.Interface != "enp1s0" {
		t.Fatalf("wizard did not enable the configured PPPoE WAN: %+v", cfg.WAN)
	}
	if cfg.LAN.Interface != "enp2s0" || cfg.LAN.IPAddress != "192.168.1.1" || cfg.LAN.CIDR != "192.168.1.1/24" {
		t.Fatalf("wizard did not preserve the configured LAN: %+v", cfg.LAN)
	}
	if !cfg.DHCP.Enabled || cfg.DHCP.RangeStart != "192.168.1.100" || cfg.DHCP.RangeEnd != "192.168.1.200" {
		t.Fatalf("fresh LAN is missing the default DHCP pool: %+v", cfg.DHCP)
	}
	if cfg.Firewall.DefaultWANInputPolicy != "deny" || !cfg.Firewall.StatefulFirewall || cfg.Firewall.WANIngressMode != "wireguard_only" {
		t.Fatalf("fresh firewall is not fail closed: %+v", cfg.Firewall)
	}
	if cfg.WireGuard.Enabled || cfg.Cloudflare.DDNSEnabled || cfg.Cloudflare.TunnelEnabled || cfg.WiFi.Enabled {
		t.Fatal("fresh setup enabled an opt-in remote or wireless service")
	}

	rules, err := services.GenerateNftables(&cfg)
	if err != nil {
		t.Fatalf("fresh firewall generation failed: %v", err)
	}
	for _, expected := range []string{
		"type filter hook input priority filter; policy drop;",
		`iifname "enp2s0" oifname "enp1s0" accept`,
		`iifname "enp2s0" oifname "ppp*" accept`,
		`oifname "enp1s0" masquerade`,
		`oifname "ppp*" masquerade`,
	} {
		if !strings.Contains(rules, expected) {
			t.Fatalf("fresh firewall/NAT is missing %q:\n%s", expected, rules)
		}
	}
	for _, forbidden := range []string{"dnat to", "tcp flags syn accept", "udp dport 51820 accept"} {
		if strings.Contains(rules, forbidden) {
			t.Fatalf("fresh setup unexpectedly exposed WAN behavior %q", forbidden)
		}
	}

	dnsmasq, err := services.GenerateDnsmasq(&cfg)
	if err != nil {
		t.Fatalf("fresh DHCP/DNS generation failed: %v", err)
	}
	for _, expected := range []string{
		"interface=enp2s0",
		"listen-address=127.0.0.1,192.168.1.1",
		"dhcp-range=192.168.1.100,192.168.1.200,255.255.255.0,12h",
		"dhcp-option=option:router,192.168.1.1",
		"dhcp-option=option:dns-server,192.168.1.1",
		"server=1.1.1.1",
		"server=1.0.0.1",
	} {
		if !strings.Contains(dnsmasq, expected) {
			t.Fatalf("fresh DHCP/DNS is missing %q:\n%s", expected, dnsmasq)
		}
	}
}
