package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// recoverWAN is the fixed WAN action, deliberately independent of config
// deltas. A service-status success alone does not prove an established PPP link.
func recoverWAN(cfg config.SystemConfig, run func(string, ...string) error, verify func() error) error {
	if !cfg.WAN.Enabled {
		return errors.New("PPPoE WAN is disabled")
	}
	if err := run("/sbin/rc-service", "pppoe-wan", "restart"); err != nil {
		return fmt.Errorf("restart PPPoE WAN: %w", err)
	}
	if err := run("/sbin/rc-service", "pppoe-wan", "status"); err != nil {
		return fmt.Errorf("PPPoE WAN service unhealthy after restart: %w", err)
	}
	if err := verify(); err != nil {
		return fmt.Errorf("PPPoE link was not recovered: %w", err)
	}
	return nil
}

func verifyWAN() error {
	return verifyWANUntil(runFixedOutput, time.Now, time.Sleep, 20*time.Second)
}

func verifyWANUntil(output func(string, ...string) (string, error), now func() time.Time, sleep func(time.Duration), timeout time.Duration) error {
	deadline := now().Add(timeout)
	for {
		address, addressErr := output("/sbin/ip", "-4", "addr", "show", "dev", "ppp0")
		route, routeErr := output("/sbin/ip", "-4", "route", "show", "default", "dev", "ppp0")
		if addressErr == nil && strings.Contains(address, "inet ") && routeErr == nil && strings.HasPrefix(strings.TrimSpace(route), "default ") {
			return nil
		}
		if !now().Before(deadline) {
			return errors.New("PPPoE interface lacks an assigned IPv4 address or default route")
		}
		sleep(500 * time.Millisecond)
	}
}
