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
		!reflect.DeepEqual(previous.DHCP.DNSServers, candidate.DHCP.DNSServers) ||
		!reflect.DeepEqual(previous.AdGuard, candidate.AdGuard) ||
		previous.System.Domain != candidate.System.Domain
}

// A restarted dnsmasq answers only after it has parsed its configuration. With
// the DNS filter categories enabled that is a ~14 MB file of ~510k domains,
// and on a 2 vCPU VM port 53 refused queries for ~200 ms after the restart
// returned — long enough for two immediate lookups to fail and roll back every
// WAN, upstream-DNS and device-profile change. Poll until the deadline instead.
// The bound stays well inside the 2-minute routerd IPC deadline even when a
// PPPoE wait and a rollback precede and follow it.
const (
	functionalDNSReadiness  = 15 * time.Second
	functionalDNSRetryEvery = 250 * time.Millisecond
)

// verifyFunctionalDNS asks the router's own dnsmasq listener to resolve public
// names. Process liveness alone is insufficient: a running dnsmasq with an
// unreachable upstream resolver leaves every LAN client effectively offline.
func verifyFunctionalDNS() error {
	return verifyFunctionalDNSAt("127.0.0.1:53", functionalDNSReadiness, functionalDNSRetryEvery)
}

func verifyFunctionalDNSAt(address string, readiness, retryEvery time.Duration) error {
	deadline := time.Now().Add(readiness)
	for {
		err := resolvePublicNames(address)
		if err == nil {
			return nil
		}
		if time.Now().Add(retryEvery).After(deadline) {
			return fmt.Errorf("local DNS could not resolve public names within %s: %w", readiness, err)
		}
		time.Sleep(retryEvery)
	}
}

func resolvePublicNames(address string) error {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: 2 * time.Second}
			return dialer.DialContext(ctx, network, address)
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
	return lastErr
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
	return verifyDDNSUpdateWithRunner(runCommandOutput)
}

func verifyDDNSUpdateWithRunner(run func(time.Duration, string, ...string) (string, error)) error {
	_, err := run(45*time.Second,
		"/usr/sbin/inadyn",
		"--once", "--force", "--foreground", "--no-pidfile",
		// Match the supervised daemon's identity so provider verification
		// cannot recreate a root-owned cache that the daemon cannot update.
		"--drop-privs", "inadyn:inadyn",
		"--config", "/etc/inadyn/inadyn.conf", "--loglevel", "notice",
	)
	if err != nil {
		return fmt.Errorf("dynamic DNS provider update failed: %w", err)
	}
	return nil
}
