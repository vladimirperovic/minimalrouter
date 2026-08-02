package api

import (
	"net"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestNextFreeWireGuardIP(t *testing.T) {
	tests := []struct {
		name   string
		server string
		subnet string
		peers  []config.WireGuardPeer
		want   string
	}{
		{
			name: "empty subnet",
			server: "10.8.0.1",
			subnet: "10.8.0.1/24",
			want: "10.8.0.2",
		},
		{
			name: "skips adjacent used",
			server: "10.8.0.1",
			subnet: "10.8.0.1/24",
			peers: []config.WireGuardPeer{
				{Enabled: true, AllowedIPs: []string{"10.8.0.2/32"}},
			},
			want: "10.8.0.3",
		},
		{
			name: "disabled peer address remains reserved",
			server: "10.8.0.1",
			subnet: "10.8.0.1/24",
			peers: []config.WireGuardPeer{
				{Enabled: false, AllowedIPs: []string{"10.8.0.2/32"}},
			},
			want: "10.8.0.3",
		},
		{
			name: "sparse hole reused",
			server: "10.8.0.1",
			subnet: "10.8.0.1/24",
			peers: []config.WireGuardPeer{
				{Enabled: true, AllowedIPs: []string{"10.8.0.2/32"}},
				{Enabled: true, AllowedIPs: []string{"10.8.0.4/32"}},
			},
			want: "10.8.0.3",
		},
		{
			name: "crosses fourth octet in slash 23",
			server: "10.8.0.254",
			subnet: "10.8.0.254/23",
			peers: []config.WireGuardPeer{
				{Enabled: true, AllowedIPs: []string{"10.8.0.255/32"}},
			},
			want: "10.8.1.0",
		},
		{
			name: "wraps to first usable address",
			server: "10.8.0.254",
			subnet: "10.8.0.254/24",
			want: "10.8.0.1",
		},
		{
			name: "slash 25 boundary",
			server: "10.8.0.126",
			subnet: "10.8.0.126/25",
			want: "10.8.0.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := nextFreeWireGuardIP(net.ParseIP(tc.server), tc.subnet, tc.peers)
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
	})
	if err == nil {
		t.Fatal("expected an error when the subnet is exhausted")
	}
}

func TestNextFreeWireGuardIPRejectsSubnetWithoutUsableClientAddress(t *testing.T) {
	for _, subnet := range []string{"10.8.0.0/31", "10.8.0.1/32"} {
		if _, err := nextFreeWireGuardIP(net.ParseIP("10.8.0.1"), subnet, nil); err == nil {
			t.Fatalf("expected %s to have no usable client address", subnet)
		}
	}
}
