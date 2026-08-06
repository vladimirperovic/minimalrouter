package recovery

import (
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestSetLANDoesNotGrantNewLANAccessToExtraLAN(t *testing.T) {
	store := testStore(t)
	current, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	current.TrustedNetworks = []string{"192.168.1.0/24", "10.8.0.0/24"}
	current.Firewall.ExtraLANs = []config.ExtraLANConfig{
		{
			ID: "wg-only-extra", Name: "wg-only", Interface: "eth3",
			CIDR: "192.168.50.0/24", RouterAddress: "192.168.50.1/24",
			DstIP: "192.168.50.10", DstPort: 443, Protocol: "tcp",
			AllowFrom: []string{"10.8.0.0/24"}, Enabled: true,
		},
	}
	current.Revision++
	if err := store.SaveConfig(current); err != nil {
		t.Fatal(err)
	}

	manager := Manager{Store: store}
	if _, err := manager.SetLAN("eth1", "10.20.30.1/24"); err != nil {
		t.Fatal(err)
	}
	cfg, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Firewall.ExtraLANs[0].AllowFrom
	if len(got) != 1 || got[0] != "10.8.0.0/24" {
		t.Fatalf("recovery LAN move widened ExtraLAN access: %v", got)
	}
}
