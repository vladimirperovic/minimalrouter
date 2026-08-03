package health

import (
	"context"
	"fmt"
	"net"
	"time"
)

// ProbeFunctionalDNS asks the router's own dnsmasq listener to resolve public
// names. Process liveness alone cannot prove clients can resolve, so this
// mirrors the privileged apply-side verification in read-only form.
func ProbeFunctionalDNS(timeout time.Duration) (ok bool, err error) {
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: 2 * time.Second}
			return dialer.DialContext(ctx, network, "127.0.0.1:53")
		},
	}
	var lastErr error
	for _, hostname := range []string{"example.com", "cloudflare.com"} {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		addresses, probeErr := resolver.LookupHost(ctx, hostname)
		cancel()
		if probeErr == nil && len(addresses) > 0 {
			return true, nil
		}
		lastErr = probeErr
	}
	if lastErr == nil {
		return false, fmt.Errorf("local DNS returned no addresses")
	}
	return false, lastErr
}
