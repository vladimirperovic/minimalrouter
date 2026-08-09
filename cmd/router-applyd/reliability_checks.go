package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

const defaultCommandTimeout = 30 * time.Second

// runCommandOutput gives every privileged child process a hard upper bound.
// router-applyd is serialized, so one wedged command must never hold the apply
// lock indefinitely and block recovery or later configuration changes.
func runCommandOutput(timeout time.Duration, binary string, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("required binary unavailable: %s", filepath.Base(binary))
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = []string{"PATH=/sbin:/usr/sbin:/bin:/usr/bin", "LANG=C", "LC_ALL=C"}
	output, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("%s timed out after %s", filepath.Base(binary), timeout)
	}
	if err != nil {
		return "", fmt.Errorf("%s failed: %s", filepath.Base(binary), sanitizeOutput(output))
	}
	return string(output), nil
}

// dnsmasqArtifactsChanged compares the actual generated runtime artifacts,
// rather than maintaining a fragile hand-written list of config sections that
// happen to affect dnsmasq today.
func dnsmasqArtifactsChanged(generated map[string]artifact, previous *config.SystemConfig) (bool, error) {
	if previous == nil {
		return true, nil
	}
	previousDNSMasq, err := services.GenerateDnsmasq(previous)
	if err != nil {
		return false, fmt.Errorf("generate previous dnsmasq configuration: %w", err)
	}
	previousAdblock := []byte("# AdGuard disabled\n")
	if previous.AdGuard.Enabled {
		previousAdblockText, err := services.GenerateAdBlockConf(previous, nil)
		if err != nil {
			return false, fmt.Errorf("generate previous DNS filter configuration: %w", err)
		}
		previousAdblock = []byte(previousAdblockText)
	}
	return !bytes.Equal(generated["dnsmasq"].data, []byte(previousDNSMasq)) ||
		!bytes.Equal(generated["adblock"].data, previousAdblock), nil
}

// requiresFunctionalDNSVerification limits the external DNS probe to changes
// that can actually affect Internet/DNS reachability. Unrelated settings should
// not fail merely because an ISP has a temporary DNS outage at that moment.
func requiresFunctionalDNSVerification(previous *config.SystemConfig, candidate config.SystemConfig) bool {
	if !candidate.WAN.Enabled {
		return false
	}
	if previous == nil {
		return true
	}
	return !reflect.DeepEqual(previous.WAN, candidate.WAN) ||
		!reflect.DeepEqual(previous.DHCP, candidate.DHCP) ||
		!reflect.DeepEqual(previous.AdGuard, candidate.AdGuard) ||
		previous.System.Domain != candidate.System.Domain
}

// verifyFunctionalDNS asks the router's own dnsmasq listener to resolve public
// names. Process liveness alone is insufficient: a running dnsmasq with an
// unreachable upstream resolver leaves every LAN client effectively offline.
func verifyFunctionalDNS() error {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: 2 * time.Second}
			return dialer.DialContext(ctx, network, "127.0.0.1:53")
		},
	}
	var lastErr error
	for _, hostname := range []string{"example.com", "cloudflare.com"} {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		addresses, err := resolver.LookupHost(ctx, hostname)
		cancel()
		if err == nil && len(addresses) > 0 {
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return errors.New("local DNS returned no addresses")
	}
	return fmt.Errorf("local DNS could not resolve public names: %w", lastErr)
}

func requiresDDNSVerification(previous *config.SystemConfig, candidate config.SystemConfig) bool {
	if !candidate.WAN.Enabled || !candidate.Cloudflare.DDNSEnabled {
		return false
	}
	if previous == nil {
		return true
	}
	return !reflect.DeepEqual(previous.Cloudflare, candidate.Cloudflare)
}

// verifyDDNSUpdate runs a bounded one-shot provider update only when the DDNS
// configuration itself changed. The normal daemon remains responsible for
// later WAN-IP changes; unrelated router changes are not coupled to provider
// availability.
func verifyDDNSUpdate() error {
	output, err := runCommandOutput(45*time.Second,
		"/usr/sbin/inadyn",
		"--once", "--force", "--foreground", "--no-pidfile",
		"--config", "/etc/inadyn/inadyn.conf", "--loglevel", "notice",
	)
	if err != nil {
		return fmt.Errorf("dynamic DNS provider update failed: %w", err)
	}
	// inadyn 2.12 exits 0 on some fatal provider responses (e.g. an HTTP 401
	// authentication failure), so the exit status alone is not a reliable
	// success signal. Treat any fatal/error response in the output as failure.
	if marker := ddnsOutputFailureMarker(output); marker != "" {
		return fmt.Errorf("dynamic DNS provider update failed: %s", marker)
	}
	return nil
}

var ddnsFailureMarkers = []string{
	"fatal error",
	"error response",
	"authentication failure",
	"error code",
	"failed connecting",
	"failed to update",
	"failed sending",
	"failed resolving",
	"timed out",
	"timeout",
	"unable",
	"unreachable",
	"cannot",
}

func ddnsOutputFailureMarker(output string) string {
	lower := strings.ToLower(output)
	for _, marker := range ddnsFailureMarkers {
		if strings.Contains(lower, marker) {
			// Provider output is external input. Keep the same sanitization boundary
			// used for non-zero child-process exits so a zero-exit provider failure
			// cannot inject control characters or unbounded raw text into audit/log
			// surfaces through the returned error.
			return sanitizeOutput([]byte(strings.TrimSpace(output)))
		}
	}
	return ""
}
