package api

import (
	"net"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestNextFreeWireGuardIP(t *testing.T) {
	server := net.ParseIP("10.8.0.1")
	cases := []struct {
		name  string
		peers []config.WireGuardPeer
		want  string
	}{
		{"empty subnet", nil, "10.8.0.2"},
		{"skips adjacent used", []config.WireGuardPeer{
			{Enabled: true, AllowedIPs: []string{"10.8.0.2/32"}},
		}, "10.8.0.3"},
		{"skips disabled peer", []config.WireGuardPeer{
			{Enabled: false, AllowedIPs: []string{"10.8.0.2/32"}},
		}, "10.8.0.2"},
		{"sparse hole reused", []config.WireGuardPeer{
			{Enabled: true, AllowedIPs: []string{"10.8.0.2/32"}},
			{Enabled: true, AllowedIPs: []string{"10.8.0.4/32"}},
		}, "10.8.0.3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := nextFreeWireGuardIP(server, "10.8.0.1/24", tc.peers)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestNextFreeWireGuardIPExhausted(t *testing.T) {
	server := net.ParseIP("10.8.0.1")
	_, err := nextFreeWireGuardIP(server, "10.8.0.1/30", []config.WireGuardPeer{
		{Enabled: true, AllowedIPs: []string{"10.8.0.2/32"}},
		{Enabled: true, AllowedIPs: []string{"10.8.0.3/32"}},
	})
	if err == nil {
		t.Fatal("expected an error when the subnet is exhausted")
	}
}
