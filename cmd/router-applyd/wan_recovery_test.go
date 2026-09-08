package main

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestWANRecoveryRestartsUnchangedConfigAndRequiresLink(t *testing.T) {
	for _, failure := range []string{"", "restart", "status", "link"} {
		t.Run(failure, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.WAN.Enabled = true
			var calls []string
			err := recoverWAN(cfg, func(binary string, args ...string) error {
				if binary != "/sbin/rc-service" || len(args) != 2 || args[0] != "pppoe-wan" {
					t.Fatal("action escaped WAN allowlist")
				}
				calls = append(calls, args[1])
				if failure == args[1] {
					return errors.New("injected")
				}
				return nil
			}, func() error {
				calls = append(calls, "link")
				if failure == "link" {
					return errors.New("no PPP address")
				}
				return nil
			})
			if (err != nil) != (failure != "") {
				t.Fatalf("wrong recovery result: %v", err)
			}
			want := []string{"restart", "status", "link"}
			if failure == "restart" {
				want = want[:1]
			}
			if failure == "status" {
				want = want[:2]
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls=%v want=%v", calls, want)
			}
		})
	}
	cfg := config.DefaultConfig()
	if err := recoverWAN(cfg, func(string, ...string) error { t.Fatal("disabled WAN was restarted"); return nil }, func() error { t.Fatal("disabled WAN verified"); return nil }); err == nil {
		t.Fatal("accepted disabled WAN recovery")
	}
}

func TestVerifyWANWaitsForAddressAndDefaultRoute(t *testing.T) {
	for _, test := range []struct {
		name           string
		address, route bool
		success        bool
	}{
		{"connected", true, true, true},
		{"service only", false, false, false},
		{"missing route", true, false, false},
		{"stale route", false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Unix(1000, 0)
			err := verifyWANUntil(func(binary string, args ...string) (string, error) {
				if binary != "/sbin/ip" || args[len(args)-1] != "ppp0" {
					t.Fatal("unexpected link query")
				}
				if args[1] == "addr" && test.address {
					return "inet 198.51.100.2/32", nil
				}
				if args[1] == "route" && test.route {
					return "default dev ppp0 scope link", nil
				}
				return "", nil
			}, func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) }, time.Second)
			if (err == nil) != test.success {
				t.Fatalf("wrong link result: %v", err)
			}
		})
	}
}
