package config

import "testing"

func TestDNSResolversValidatedWithoutDHCP(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, servers := range [][]string{nil, {"invalid"}, {"1.1.1.1\n# injected"}, {"127.0.0.1"}, {"192.168.1.1"}, {"224.0.0.1"}, {"1.1.1.1", "1.1.1.1"}, {"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4"}} {
			cfg := DefaultConfig()
			cfg.DHCP.Enabled = enabled
			cfg.DHCP.DNSServers = servers
			if err := cfg.Validate(); err == nil {
				t.Fatalf("enabled=%v accepted upstreams %q", enabled, servers)
			}
		}
		for _, servers := range [][]string{{"1.1.1.1"}, {"1.1.1.1", "9.9.9.9"}, {"1.1.1.1", "9.9.9.9", "8.8.8.8"}} {
			cfg := DefaultConfig()
			cfg.DHCP.Enabled = enabled
			cfg.DHCP.DNSServers = servers
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestLANGatewayMustBeUsableHost(t *testing.T) {
	for _, address := range []string{"192.168.1.0", "192.168.1.255"} {
		cfg := DefaultConfig()
		cfg.LAN.IPAddress = address
		cfg.LAN.CIDR = address + "/24"
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted %s", address)
		}
	}
	cfg := DefaultConfig()
	cfg.LAN.IPAddress = "192.168.1.254"
	cfg.LAN.CIDR = "192.168.1.254/24"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
