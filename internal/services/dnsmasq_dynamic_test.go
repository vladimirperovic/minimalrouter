package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestGenerateDnsmasqUsesDynamicBindingForWireGuard(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.Interface = "wg0"
	cfg.WireGuard.Address = "10.8.0.1/24"

	got, err := GenerateDnsmasq(&cfg)
	if err != nil {
		t.Fatalf("GenerateDnsmasq: %v", err)
	}
	for _, want := range []string{"interface=wg0", "listen-address=127.0.0.1,192.168.1.1,10.8.0.1", "bind-dynamic"} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated dnsmasq config missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "bind-interfaces") {
		t.Fatal("bind-interfaces makes cold boot depend on wg0 existing before dnsmasq starts")
	}
}
