package telemetry

import "testing"

func TestDDNSInSync(t *testing.T) {
	cases := []struct {
		name     string
		lastIP   string
		resolved string
		want     bool
	}{
		{"equal IPv4", "93.86.125.80", "93.86.125.80", true},
		{"drifted provider state", "93.86.125.80", "109.92.0.162", false},
		{"missing last IP", "", "93.86.125.80", false},
		{"failed lookup is unknown not mismatch", "93.86.125.80", "", false},
		{"both missing", "", "", false},
		{"garbage input", "not-an-ip", "93.86.125.80", false},
		{"whitespace tolerated", " 93.86.125.80 ", "93.86.125.80", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ddnsInSync(tc.lastIP, tc.resolved); got != tc.want {
				t.Fatalf("ddnsInSync(%q, %q) = %v, want %v", tc.lastIP, tc.resolved, got, tc.want)
			}
		})
	}
}

func TestResolveDDNSHostnameLocalhost(t *testing.T) {
	ip, ok := resolveDDNSHostname("localhost")
	if !ok {
		t.Fatal("localhost did not resolve")
	}
	if ip != "127.0.0.1" {
		t.Fatalf("localhost resolved to %q, want 127.0.0.1", ip)
	}
}

func TestResolveDDNSHostnameInvalid(t *testing.T) {
	if _, ok := resolveDDNSHostname("invalid.invalid"); ok {
		t.Fatal("unresolvable hostname reported success")
	}
}
