package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestRecoverySSHIsManagementPlaneOnly(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Interface = "eth0"
	cfg.WAN.Enabled = true
	cfg.LAN.Interface = "eth1"
	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatalf("GenerateNftables: %v", err)
	}
	if !strings.Contains(rules, `iifname "eth1" tcp dport 22 accept`) {
		t.Fatal("SSH missing from LAN")
	}
	if strings.Contains(rules, `iifname "eth0" tcp dport 22 accept`) || strings.Contains(rules, `iifname "ppp*" tcp dport 22 accept`) {
		t.Fatal("SSH exposed on WAN")
	}
}

func TestRecoverySSHRespectsWireGuardOnly(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Interface = "eth0"
	cfg.WAN.Enabled = true
	cfg.LAN.Interface = "eth1"
	cfg.System.ManagementAccess = "wireguard_only"
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.Interface = "wg0"
	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatalf("GenerateNftables: %v", err)
	}
	if strings.Contains(rules, `iifname "eth1" tcp dport 22 accept`) {
		t.Fatal("SSH exposed on LAN in wireguard_only mode")
	}
	if !strings.Contains(rules, `iifname "wg0" tcp dport 22 accept`) {
		t.Fatal("SSH missing from WireGuard")
	}
}
