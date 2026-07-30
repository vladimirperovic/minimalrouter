//go:build ignore

// This command is invoked only by CI to render a representative IoT/schedule
// configuration for real Alpine nftables and dnsmasq parser checks.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/render-policy-fixture.go OUTPUT_DIR")
		os.Exit(2)
	}
	cfg := config.DefaultConfig()
	cfg.System.Timezone = "Europe/Belgrade"
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "ci-user"
	cfg.WAN.Password = "ci-password"
	cfg.IoT.Enabled = true
	cfg.IoT.Mode = "vlan"
	cfg.IoT.ParentInterface = cfg.LAN.Interface
	cfg.IoT.VLANID = 30
	cfg.IoT.DHCP.StaticLeases = []config.StaticLease{{
		ID: "camera", Hostname: "camera", MAC: "02:00:00:00:30:10", IPAddress: "192.168.30.50",
	}}
	cfg.DHCP.StaticLeases = []config.StaticLease{{
		ID: "kids-tablet", Hostname: "kids-tablet", MAC: "02:00:00:00:00:10", IPAddress: "192.168.1.50",
	}}
	cfg.Policies = config.DevicePolicyConfig{
		Enabled: true,
		Profiles: []config.DeviceProfile{{
			ID: "kids-evening", Name: "Kids evening", Enabled: true,
			AccessMode: "allow_services", AllowedServices: []string{"youtube", "steam"},
			Windows: []config.AccessWindow{
				{Days: []string{"monday", "tuesday", "wednesday", "thursday", "friday"}, Start: "19:00", End: "23:59"},
				{Days: []string{"saturday", "sunday"}, AllDay: true},
			},
		}},
		Assignments: []config.DeviceAssignment{{
			ID: "kids-tablet", Hostname: "kids-tablet", MAC: "02:00:00:00:00:10",
			IPAddress: "192.168.1.50", Zone: "lan", ProfileID: "kids-evening",
		}},
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "fixture validation: %v\n", err)
		os.Exit(1)
	}
	nft, err := services.GenerateNftables(&cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate nftables: %v\n", err)
		os.Exit(1)
	}
	dns, err := services.GenerateDnsmasq(&cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate dnsmasq: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(os.Args[1], 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(os.Args[1], "iot-policy.nft"), []byte(nft), 0600); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(os.Args[1], "iot-policy.dnsmasq"), []byte(dns), 0600); err != nil {
		panic(err)
	}
}
