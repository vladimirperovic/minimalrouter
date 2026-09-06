package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// Interface replacement can leave dnsmasq without its tunnel bindings despite
// bind-dynamic. Reconcile that dependency while the caller holds applyMu;
// neither interface existence nor an OpenRC status proves DNS readiness.
func restartWireGuardServices(cfg config.SystemConfig,
	server, client func(config.SystemConfig) error,
	output func(string, ...string) (string, error),
	run func(string, ...string) error, dns func(string) error,
) error {
	tunnelErr := func() error {
		if err := server(cfg); err != nil {
			return fmt.Errorf("restart WireGuard server: %w", err)
		}
		if err := client(cfg); err != nil {
			return fmt.Errorf("restart WireGuard client: %w", err)
		}
		if cfg.WireGuard.Enabled {
			if err := verifyRestartedWireGuardAddress(wireGuardInterfaceName(cfg.WireGuard), cfg.WireGuard.Address, output); err != nil {
				return fmt.Errorf("WireGuard server unavailable after restart: %w", err)
			}
		}
		if cfg.WGClient.Enabled {
			if err := verifyRestartedWireGuardAddress(wireGuardClientInterfaceName(cfg.WGClient), cfg.WGClient.Address, output); err != nil {
				return fmt.Errorf("WireGuard client unavailable after restart: %w", err)
			}
		}
		return nil
	}()
	// Activation can fail after deleting/replacing an interface. Still attempt
	// to restore DNS for the remaining interfaces; never mask the tunnel error.
	dnsErr := func() error {
		if err := run("/sbin/rc-service", "dnsmasq", "restart"); err != nil {
			return fmt.Errorf("restart DNS after WireGuard: %w", err)
		}
		if err := run("/sbin/rc-service", "dnsmasq", "status"); err != nil {
			return fmt.Errorf("DNS unhealthy after WireGuard restart: %w", err)
		}
		address := "127.0.0.1"
		if cfg.WireGuard.Enabled {
			prefix, err := netip.ParsePrefix(cfg.WireGuard.Address)
			if err != nil || !prefix.Addr().Is4() {
				return errors.New("invalid trusted WireGuard DNS address")
			}
			address = prefix.Addr().String()
		}
		if err := dns(address); err != nil {
			return fmt.Errorf("DNS listener unavailable at %s after WireGuard restart: %w", address, err)
		}
		return nil
	}()
	return errors.Join(tunnelErr, dnsErr)
}

func verifyRestartedWireGuardAddress(iface, address string, output func(string, ...string) (string, error)) error {
	text, err := output("/sbin/ip", "-o", "-4", "address", "show", "dev", iface)
	if err != nil {
		return err
	}
	// Outbound tunnels may intentionally have no local IPv4 address.
	if address == "" {
		return nil
	}
	want, err := netip.ParsePrefix(address)
	if err != nil || !want.Addr().Is4() {
		return errors.New("invalid trusted WireGuard interface address")
	}
	fields := strings.Fields(text)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "inet" {
			if got, err := netip.ParsePrefix(fields[i+1]); err == nil && got == want {
				return nil
			}
		}
	}
	return fmt.Errorf("%s lacks configured address %s", iface, address)
}

func verifyWireGuardDNSListener(address string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	var lastErr error
	for {
		attempt, stop := context.WithTimeout(ctx, 500*time.Millisecond)
		lastErr = probeWireGuardDNS(attempt, net.JoinHostPort(address, "53"), dialer.DialContext)
		stop()
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return lastErr
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// A random single-label A question is answered locally by generated dnsmasq's
// domain-needed policy. NOERROR or NXDOMAIN proves a DNS response at this exact
// address without depending on WAN/upstream availability, libc or /etc/hosts.
// This is listener readiness, not proof of a peer handshake or upstream DNS.
func probeWireGuardDNS(ctx context.Context, address string, dial func(context.Context, string, string) (net.Conn, error)) error {
	var nonce [10]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	name := "mr-ready-" + hex.EncodeToString(nonce[2:])
	query := make([]byte, 12)
	copy(query[:2], nonce[:2])
	query[2], query[5] = 1, 1 // recursion desired, one question
	query = append(query, byte(len(name)))
	query = append(query, name...)
	query = append(query, 0, 0, 1, 0, 1) // root, A, IN
	conn, err := dial(ctx, "udp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return err
		}
	}
	if _, err := conn.Write(query); err != nil {
		return err
	}
	var response [4096]byte
	n, err := conn.Read(response[:])
	if err != nil {
		return err
	}
	if n < len(query) || !bytes.Equal(response[:2], query[:2]) ||
		response[2]&0xfa != 0x80 || // response, normal opcode, not truncated
		binary.BigEndian.Uint16(response[4:6]) != 1 ||
		!bytes.Equal(response[12:len(query)], query[12:]) {
		return errors.New("invalid DNS readiness response")
	}
	if code := response[3] & 15; code != 0 && code != 3 {
		return fmt.Errorf("DNS readiness response code %d", code)
	}
	return nil
}
