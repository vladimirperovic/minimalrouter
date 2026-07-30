package network

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverFromPrefersDefaultRouteForWANAndPhysicalCarrierForLAN(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"enp1s0", "enp2s0"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "carrier"), []byte("1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	routes := filepath.Join(t.TempDir(), "route")
	if err := os.WriteFile(routes, []byte("Iface\tDestination\tGateway\tFlags\n"+
		"enp1s0\t00000000\t0101A8C0\t0003\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := DiscoverFrom(root, routes, []net.Interface{
		{Name: "lo", Flags: net.FlagUp | net.FlagLoopback},
		{Name: "enp2s0", Flags: net.FlagUp, HardwareAddr: mustMAC(t, "02:00:00:00:00:02")},
		{Name: "enp1s0", Flags: net.FlagUp, HardwareAddr: mustMAC(t, "02:00:00:00:00:01")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.WAN != "enp1s0" || result.LAN != "enp2s0" {
		t.Fatalf("unexpected recommendation: WAN=%s LAN=%s", result.WAN, result.LAN)
	}
}

func TestDiscoverFromRejectsSingleUsableInterface(t *testing.T) {
	_, err := DiscoverFrom(t.TempDir(), filepath.Join(t.TempDir(), "missing"), []net.Interface{{Name: "eth0"}})
	if err == nil {
		t.Fatal("single-interface system was accepted")
	}
}

func TestEligibleNameRejectsVirtualAndTunnelInterfaces(t *testing.T) {
	for _, name := range []string{"lo", "docker0", "vethabc", "wg0", "ppp0", "tailscale0", "br-lan"} {
		if eligibleName(name) {
			t.Fatalf("virtual interface %q was considered eligible", name)
		}
	}
	if !eligibleName("enp3s0") {
		t.Fatal("physical-style interface was rejected")
	}
}

func mustMAC(t *testing.T, value string) net.HardwareAddr {
	t.Helper()
	mac, err := net.ParseMAC(value)
	if err != nil {
		t.Fatal(err)
	}
	return mac
}
