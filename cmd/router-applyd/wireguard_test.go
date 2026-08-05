package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

func TestWireGuardMTUAccountsForPPPoEAndTunnelOverhead(t *testing.T) {
	cfg := config.DefaultConfig()

	cfg.WAN.MTU = 1492
	if got := wireGuardMTU(cfg); got != 1412 {
		t.Fatalf("PPPoE WireGuard MTU = %d, want 1412", got)
	}

	cfg.WAN.MTU = 1500
	if got := wireGuardMTU(cfg); got != 1420 {
		t.Fatalf("Ethernet WireGuard MTU = %d, want 1420", got)
	}

	cfg.WAN.MTU = 1280
	if got := wireGuardMTU(cfg); got != 1200 {
		t.Fatalf("small-underlay WireGuard MTU = %d, want 1200", got)
	}
}

func TestBashlessWireGuardLifecycleIntegration(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("MINIMALROUTER_WIREGUARD_INTEGRATION") != "1" {
		t.Skip("requires an isolated root Linux network namespace")
	}

	const (
		clientNamespace = "mrwgclient"
		serverUnderlay  = "mrwgwan"
		clientUnderlay  = "mrwghost"
		clientInterface = "mrwgpeer"
	)

	_ = removeWireGuard("wg0")
	_ = runFixed("/sbin/ip", "netns", "delete", clientNamespace)
	t.Cleanup(func() {
		_ = removeWireGuard("wg0")
		_ = runFixed("/sbin/ip", "netns", "delete", clientNamespace)
	})

	serverPrivate, serverPublic, err := services.GenerateWireGuardKeypair()
	if err != nil {
		t.Fatal(err)
	}
	clientPrivate, clientPublic, err := services.GenerateWireGuardKeypair()
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.WAN.MTU = 1492
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.PrivateKey = serverPrivate
	cfg.WireGuard.Peers = []config.WireGuardPeer{{
		ID:         "integration-peer",
		Name:       "integration-peer",
		PublicKey:  clientPublic,
		AllowedIPs: []string{"10.8.0.2/32"},
		Endpoint:   "172.31.0.2:51821",
		Enabled:    true,
	}}

	runtimeConfig, err := services.GenerateWireGuardRuntime(&cfg.WireGuard)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(wireGuardRuntimePath), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wireGuardRuntimePath, []byte(runtimeConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := preflightWireGuard(wireGuardRuntimePath); err != nil {
		t.Fatalf("preflight failed: %v", err)
	}

	for _, command := range [][]string{
		{"netns", "add", clientNamespace},
		{"link", "add", serverUnderlay, "type", "veth", "peer", "name", clientUnderlay},
		{"link", "set", clientUnderlay, "netns", clientNamespace},
		{"address", "add", "172.31.0.1/24", "dev", serverUnderlay},
		{"link", "set", "dev", serverUnderlay, "up"},
		{"netns", "exec", clientNamespace, "/sbin/ip", "address", "add", "172.31.0.2/24", "dev", clientUnderlay},
		{"netns", "exec", clientNamespace, "/sbin/ip", "link", "set", "dev", "lo", "up"},
		{"netns", "exec", clientNamespace, "/sbin/ip", "link", "set", "dev", clientUnderlay, "up"},
		{"netns", "exec", clientNamespace, "/sbin/ip", "link", "add", "dev", clientInterface, "type", "wireguard"},
	} {
		if err := runFixed("/sbin/ip", command...); err != nil {
			t.Fatalf("ip %s: %v", strings.Join(command, " "), err)
		}
	}

	clientConfig := fmt.Sprintf(
		"[Interface]\nPrivateKey = %s\nListenPort = 51821\n\n"+
			"[Peer]\nPublicKey = %s\nAllowedIPs = 10.8.0.1/32\n"+
			"Endpoint = 172.31.0.1:51820\nPersistentKeepalive = 25\n",
		clientPrivate, serverPublic,
	)
	clientPath := filepath.Join(t.TempDir(), "client.conf")
	if err := os.WriteFile(clientPath, []byte(clientConfig), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{
		{"netns", "exec", clientNamespace, "/usr/bin/wg", "setconf", clientInterface, clientPath},
		{"netns", "exec", clientNamespace, "/sbin/ip", "address", "add", "10.8.0.2/24", "dev", clientInterface},
		{"netns", "exec", clientNamespace, "/sbin/ip", "link", "set", "dev", clientInterface, "mtu", "1412", "up"},
		{"netns", "exec", clientNamespace, "/sbin/ip", "route", "replace", "10.8.0.1/32", "dev", clientInterface},
	} {
		if err := runFixed("/sbin/ip", command...); err != nil {
			t.Fatalf("client setup %s: %v", strings.Join(command, " "), err)
		}
	}

	if err := activateWireGuard(cfg); err != nil {
		t.Fatalf("activation failed: %v", err)
	}
	if err := runFixed("/bin/ping", "-c", "5", "-W", "2", "10.8.0.2"); err != nil {
		t.Fatalf("encrypted packet test failed: %v", err)
	}

	link, err := runFixedOutput("/sbin/ip", "-o", "link", "show", "dev", "wg0")
	if err != nil || !strings.Contains(link, "mtu 1412") {
		t.Fatalf("unexpected WireGuard link: %q (%v)", link, err)
	}
	route, err := runFixedOutput("/sbin/ip", "-4", "route", "show", "10.8.0.2/32")
	if err != nil || !strings.Contains(route, "dev wg0") {
		t.Fatalf("WireGuard peer route missing: %q (%v)", route, err)
	}
	handshakes, err := runFixedOutput("/usr/bin/wg", "show", "wg0", "latest-handshakes")
	if err != nil || strings.HasSuffix(strings.TrimSpace(handshakes), "\t0") {
		t.Fatalf("WireGuard handshake missing: %q (%v)", handshakes, err)
	}
}

func wgDump(handshake int64) string {
	return fmt.Sprintf("interface\tprivkey\t51820\t0\nABCpubkey\t(off)\t203.0.113.9:51820\t10.6.0.0/24\t%d\t1234\t5678\t25\n", handshake)
}

func TestWGHandshakeFresh(t *testing.T) {
	fresh := time.Now().Unix() - 30
	if err := wgHandshakeFresh(wgDump(fresh), 25); err != nil {
		t.Fatalf("fresh handshake rejected: %v", err)
	}
	if err := wgHandshakeFresh(wgDump(fresh), 0); err != nil {
		t.Fatalf("keepalive-0 fresh handshake rejected: %v", err)
	}
	stale := time.Now().Unix() - 10*60
	if err := wgHandshakeFresh(wgDump(stale), 25); err == nil {
		t.Fatal("stale handshake accepted with keepalive")
	}
	if err := wgHandshakeFresh(wgDump(stale), 0); err != nil {
		t.Fatalf("keepalive-0 idle tunnel needlessly rejected: %v", err)
	}
	if err := wgHandshakeFresh("interface\tprivkey\t51820\t0\nABCpubkey\t(off)\t203.0.113.9:51820\t10.6.0.0/24\t0\t0\t0\toff\n", 25); err == nil {
		t.Fatal("peer with zero latest-handshake accepted with keepalive")
	}
}

func TestWGHandshakeRejectsNoPeer(t *testing.T) {
	if err := wgHandshakeFresh("interface\tprivkey\t51820\t0\n", 25); err == nil {
		t.Fatal("tunnel without a peer accepted")
	}
}

func TestRemovedAllowedIPs(t *testing.T) {
	old := []string{"10.8.0.2/32", "10.8.0.3/32", "10.8.0.4/32"}
	cur := []string{"10.8.0.2/32", "10.8.0.5/32"}
	got := removedAllowedIPs(old, cur)
	if len(got) != 2 {
		t.Fatalf("stale routes = %v, want 2", got)
	}
	for _, want := range []string{"10.8.0.3/32", "10.8.0.4/32"} {
		found := false
		for _, r := range got {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("stale route %s missing from %v", want, got)
		}
	}
	// Plain host addresses and CIDR variants normalize to the same key.
	if got := removedAllowedIPs([]string{"10.8.0.9", "10.8.0.0/24"}, []string{"10.8.0.9/32", "10.8.0.0/24"}); len(got) != 0 {
		t.Fatalf("normalized duplicates reported stale: %v", got)
	}
}

func TestParseWGTunnelStatusSanitizesKeys(t *testing.T) {
	dump := "interface\tINTERFACE-PRIVATE-KEY\t51820\t0\n" +
		"PEER-PUBLIC-KEY\tPEER-PRESHARED-KEY\t203.0.113.9:51820\t10.6.0.0/24\t1750000000\t1234\t5678\t25\n"
	status := parseWGTunnelStatus("wg1", dump)
	if status.LastHandshake != 1750000000 || status.RxBytes != 1234 || status.TxBytes != 5678 {
		t.Fatalf("status fields not projected: %+v", status)
	}
	if status.Endpoint != "203.0.113.9:51820" {
		t.Fatalf("endpoint %q", status.Endpoint)
	}
	serialized := fmt.Sprintf("%+v", status)
	for _, secret := range []string{"PEER-PUBLIC-KEY", "PEER-PRESHARED-KEY", "INTERFACE-PRIVATE-KEY"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("WireGuard key crossed the privilege boundary: %q", secret)
		}
	}
}
