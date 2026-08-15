package health

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type stubDNSResolver func(context.Context, string) ([]string, error)

func (stub stubDNSResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return stub(ctx, host)
}

func TestProbeFunctionalDNSAcceptsFreshNXDOMAINResponse(t *testing.T) {
	resolver := stubDNSResolver(func(_ context.Context, host string) ([]string, error) {
		if host != "fresh.example.com." {
			t.Fatalf("probe name = %q", host)
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	})

	ok, err := probeFunctionalDNS(resolver, time.Second, "fresh.example.com.")
	if err != nil || !ok {
		t.Fatalf("fresh NXDOMAIN response should prove upstream availability: ok=%v err=%v", ok, err)
	}
}

func TestProbeFunctionalDNSRejectsTimeout(t *testing.T) {
	wantErr := errors.New("upstream timeout")
	resolver := stubDNSResolver(func(context.Context, string) ([]string, error) {
		return nil, wantErr
	})

	ok, err := probeFunctionalDNS(resolver, time.Second, "fresh.example.com.")
	if ok || !errors.Is(err, wantErr) {
		t.Fatalf("timeout should degrade DNS health: ok=%v err=%v", ok, err)
	}
}

func TestProbeFunctionalDNSAcceptsResolvedAddress(t *testing.T) {
	resolver := stubDNSResolver(func(context.Context, string) ([]string, error) {
		return []string{"192.0.2.1"}, nil
	})

	ok, err := probeFunctionalDNS(resolver, time.Second, "fresh.example.com.")
	if err != nil || !ok {
		t.Fatalf("resolved address should be healthy: ok=%v err=%v", ok, err)
	}
}
