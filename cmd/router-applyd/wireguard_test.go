package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
