package api

import (
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
	"testing"
)

func TestDeviceLabelsUseOnlyLeasesAndPreferNamedReservations(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DHCP.StaticLeases = []config.StaticLease{
		{IPAddress: "192.168.1.20", Hostname: "configured", MAC: "02:00:00:00:00:20"},
		{IPAddress: "192.168.1.21", MAC: "02:00:00:00:00:21"},
	}
	leases := []telemetry.DHCPLease{
		{IPAddress: "192.168.1.20", Hostname: "dynamic", MAC: "02:00:00:00:00:30"},
		{IPAddress: "192.168.1.21", Hostname: "learned", MAC: "02:00:00:00:00:21"},
		{IPAddress: "192.168.1.100", Hostname: "laptop", MAC: "02:00:00:00:01:00"},
	}
	labels := deviceLabelsFromLeases(cfg, leases)
	if labels["192.168.1.20"].hostname != "configured" || labels["192.168.1.20"].mac != "02:00:00:00:00:20" {
		t.Fatal("live lease overrode named reservation")
	}
	if labels["192.168.1.21"].hostname != "learned" || labels["192.168.1.100"].hostname != "laptop" {
		t.Fatal("live label missing")
	}
	delete(labels, "192.168.1.20")
	if deviceLabelsFromLeases(cfg, nil)["192.168.1.20"].hostname != "configured" {
		t.Fatal("labels mutated configuration")
	}
	if _, ok := deviceLabelsFromLeases(cfg, nil)["192.168.1.100"]; ok {
		t.Fatal("expired live label retained")
	}
}
