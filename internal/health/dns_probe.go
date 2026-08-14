package health

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

type dnsHostResolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

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
	probeName := "minimalrouter-health-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ".example.com."
	return probeFunctionalDNS(resolver, timeout, probeName)
}

func probeFunctionalDNS(resolver dnsHostResolver, timeout time.Duration, probeName string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	addresses, err := resolver.LookupHost(ctx, probeName)
	if err == nil {
		if len(addresses) > 0 {
			return true, nil
		}
		return false, fmt.Errorf("local DNS returned no response")
	}

	// A valid NXDOMAIN proves that the query reached a recursive upstream. The
	// randomized name deliberately bypasses dnsmasq's positive and negative
	// cache, so a cached answer cannot hide an ISP resolver outage.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return true, nil
	}
	return false, err
}
