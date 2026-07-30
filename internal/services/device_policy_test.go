package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func policyTestConfig() config.SystemConfig {
	cfg := config.DefaultConfig()
	cfg.System.Timezone = "Europe/Belgrade"
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test-user"
	cfg.WAN.Password = "test-password"
	cfg.IoT.Enabled = true
	cfg.IoT.Interface = "eth2"
	cfg.IoT.DHCP.StaticLeases = []config.StaticLease{{
		ID: "camera", Hostname: "camera", MAC: "02:00:00:00:30:10", IPAddress: "192.168.30.50",
	}}
	cfg.DHCP.StaticLeases = []config.StaticLease{{
		ID: "kid-tablet", Hostname: "kid-tablet", MAC: "02:00:00:00:00:10", IPAddress: "192.168.1.50",
	}}
	cfg.Policies = config.DevicePolicyConfig{
		Enabled: true,
		Profiles: []config.DeviceProfile{
			{
				ID: "kids-evening", Name: "Kids evening", Enabled: true,
				AccessMode: "allow_services", AllowedServices: []string{"youtube", "steam"},
				Windows: []config.AccessWindow{
					{Days: []string{"monday", "tuesday", "wednesday", "thursday", "friday"}, Start: "19:00", End: "23:59"},
					{Days: []string{"saturday", "sunday"}, AllDay: true},
				},
			},
			{
				ID: "iot-online", Name: "IoT online", Enabled: true, AccessMode: "allow_all",
				Windows: []config.AccessWindow{{Days: []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}, AllDay: true}},
			},
		},
		Assignments: []config.DeviceAssignment{
			{ID: "kid-tablet", Hostname: "kid-tablet", MAC: "02:00:00:00:00:10", IPAddress: "192.168.1.50", Zone: "lan", ProfileID: "kids-evening"},
			{ID: "camera", Hostname: "camera", MAC: "02:00:00:00:30:10", IPAddress: "192.168.30.50", Zone: "iot", ProfileID: "iot-online"},
		},
	}
	return cfg
}

func TestGenerateDnsmasqAddsIsolatedIoTAndPolicySets(t *testing.T) {
	cfg := policyTestConfig()
	out, err := GenerateDnsmasq(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"interface=eth2",
		"listen-address=127.0.0.1,192.168.1.1,192.168.30.1",
		"dhcp-range=set:iot,192.168.30.100,192.168.30.200,255.255.255.0,12h",
		"dhcp-option=tag:iot,option:router,192.168.30.1",
		"dhcp-host=02:00:00:00:30:10,set:iot,192.168.30.50,camera",
		"4#inet#minimalrouter#svc_youtube",
		"4#inet#minimalrouter#svc_steam",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("dnsmasq output missing %q:\n%s", expected, out)
		}
	}
}

func TestGenerateNftablesIsolatesIoTAndEnforcesKidsSchedule(t *testing.T) {
	cfg := policyTestConfig()
	out, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"set svc_youtube",
		"set svc_steam",
		`iifname "eth2" oifname "eth1" drop`,
		`iifname "eth1" oifname "eth2" drop`,
		`iifname "eth2" oifname "eth0" accept`,
		`meta day { "Monday", "Tuesday", "Wednesday", "Thursday", "Friday" } meta hour "19:00:00"-"23:59:59"`,
		`meta day { "Saturday", "Sunday" } ip daddr @svc_youtube`,
		`ip daddr @svc_steam`,
		`iifname "eth1" ip saddr 192.168.1.50 drop`,
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("nftables output missing %q:\n%s", expected, out)
		}
	}

	isolation := strings.Index(out, `iifname "eth2" oifname "eth1" drop`)
	deviceDrop := strings.Index(out, `iifname "eth1" ip saddr 192.168.1.50 drop`)
	established := strings.Index(out, "ct state established,related accept")
	// Use the established-state rule in the forward chain, not the earlier input rule.
	established = strings.Index(out[strings.Index(out, "chain forward"):], "ct state established,related accept") + strings.Index(out, "chain forward")
	if isolation < 0 || deviceDrop < 0 || established < 0 || isolation > established || deviceDrop > established {
		t.Fatalf("zone and device rejection must precede established-state acceptance:\n%s", out)
	}
}

func TestPoliciesRemainInactiveWhenDisabled(t *testing.T) {
	cfg := policyTestConfig()
	cfg.Policies.Enabled = false
	dns, err := GenerateDnsmasq(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	nft, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dns, "nftset=") || strings.Contains(nft, "Device policy:") {
		t.Fatal("disabled policies generated active DNS or firewall rules")
	}
}
