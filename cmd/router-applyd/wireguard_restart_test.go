package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestWireGuardRestartReconcilesDNSAfterInterfaceReplacement(t *testing.T) {
	for _, failure := range []string{"", "server", "client", "wg0", "wg1", "restart", "status", "dns"} {
		t.Run(failure, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.WireGuard.Enabled = true
			cfg.WireGuard.Interface, cfg.WireGuard.Address = "wg0", "10.8.0.1/24"
			cfg.WGClient.Enabled = true
			cfg.WGClient.Interface, cfg.WGClient.Address = "wg1", "10.7.0.2/32"
			injected := errors.New("injected " + failure)
			var calls []string
			step := func(name string) error {
				calls = append(calls, name)
				if name == failure {
					return injected
				}
				return nil
			}
			err := restartWireGuardServices(cfg,
				func(config.SystemConfig) error { return step("server") },
				func(config.SystemConfig) error { return step("client") },
				func(binary string, args ...string) (string, error) {
					iface := args[len(args)-1]
					if binary != "/sbin/ip" || !reflect.DeepEqual(args, []string{"-o", "-4", "address", "show", "dev", iface}) {
						t.Fatal("escaped fixed interface verification")
					}
					address := cfg.WireGuard.Address
					if iface == "wg1" {
						address = cfg.WGClient.Address
					}
					return "3: " + iface + " inet " + address + " scope global " + iface, step(iface)
				},
				func(binary string, args ...string) error {
					if binary != "/sbin/rc-service" || len(args) != 2 || args[0] != "dnsmasq" {
						t.Fatal("escaped DNS service allowlist")
					}
					return step(args[1])
				},
				func(address string) error {
					if address != "10.8.0.1" {
						t.Fatalf("probed %s, not the replaced WG listener", address)
					}
					return step("dns")
				})
			want := []string{"server", "client", "wg0", "wg1"}
			for i, name := range want {
				if name == failure {
					want = want[:i+1]
					break
				}
			}
			for _, name := range []string{"restart", "status", "dns"} {
				want = append(want, name)
				if name == failure {
					break
				}
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls=%v want=%v", calls, want)
			}
			if failure == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, injected) {
					t.Fatalf("lost dependency failure: %v", err)
				}
				response := serviceActionFailure(err)
				if response.Success || response.Code != apply.ActionFailed {
					t.Fatalf("failure reported as success: %+v", response)
				}
			}
		})
	}
}

func TestWireGuardRestartPreservesBothFailuresAndRepairsDNSWhenDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WireGuard.Enabled, cfg.WGClient.Enabled = false, false
	tunnelErr, dnsErr := errors.New("interface cleanup failed"), errors.New("DNS failed")
	var addresses []string
	err := restartWireGuardServices(cfg,
		func(config.SystemConfig) error { return tunnelErr },
		func(config.SystemConfig) error { t.Fatal("continued activation after failure"); return nil },
		func(string, ...string) (string, error) { t.Fatal("verified disabled interface"); return "", nil },
		func(string, ...string) error { return nil },
		func(address string) error { addresses = append(addresses, address); return dnsErr })
	if !errors.Is(err, tunnelErr) || !errors.Is(err, dnsErr) || !reflect.DeepEqual(addresses, []string{"127.0.0.1"}) {
		t.Fatalf("partial recovery: %v, %v", err, addresses)
	}
}

func TestWireGuardRestartRequiresExactConfiguredAddress(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		wantOK       bool
	}{
		{"present", "3: wg0 inet 10.8.0.1/24 scope global wg0", true},
		{"link only", "3: wg0 <POINTOPOINT,UP> mtu 1420", false},
		{"wrong IP", "3: wg0 inet 10.8.0.10/24 scope global wg0", false},
		{"wrong prefix", "3: wg0 inet 10.8.0.1/32 scope global wg0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyRestartedWireGuardAddress("wg0", "10.8.0.1/24", func(string, ...string) (string, error) { return tc.output, nil })
			if (err == nil) != tc.wantOK {
				t.Fatalf("address verification: %v", err)
			}
		})
	}
}

func TestWireGuardDNSProbeRequiresMatchingDNSResponse(t *testing.T) {
	for _, mode := range []string{"nxdomain", "noerror", "servfail", "refused", "wrong-id", "wrong-question", "wrong-count", "truncated", "query", "short", "silent", "missing-listener"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			serverDone := make(chan error, 1)
			err := probeWireGuardDNS(ctx, "10.8.0.1:53", func(_ context.Context, network, address string) (net.Conn, error) {
				if network != "udp" || address != "10.8.0.1:53" {
					t.Fatalf("wrong DNS destination %s %s", network, address)
				}
				if mode == "missing-listener" {
					serverDone <- nil
					return nil, errors.New("connection refused")
				}
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					var packet [512]byte
					n, err := server.Read(packet[:])
					if err != nil {
						serverDone <- err
						return
					}
					response := packet[:n]
					if n < 18 || int(response[12])+18 != n || strings.Contains(string(response[13:n-5]), ".") {
						serverDone <- fmt.Errorf("not a local single-label DNS question: %x", response)
						return
					}
					response[2], response[3] = 0x81, 0x83
					switch mode {
					case "noerror":
						response[3] = 0x80
					case "servfail":
						response[3] = 0x82
					case "refused":
						response[3] = 0x85
					case "wrong-id":
						response[0] ^= 1
					case "wrong-question":
						response[14] ^= 1
					case "wrong-count":
						response[5] = 0
					case "truncated":
						response[2] |= 2
					case "query":
						response[2] &^= 0x80
					case "short":
						response = response[:11]
					case "silent":
						<-ctx.Done()
						serverDone <- nil
						return
					}
					_, err = server.Write(response)
					serverDone <- err
				}()
				return client, nil
			})
			if serverErr := <-serverDone; serverErr != nil {
				t.Fatal(serverErr)
			}
			wantOK := mode == "nxdomain" || mode == "noerror"
			if (err == nil) != wantOK {
				t.Fatalf("DNS probe result: %v", err)
			}
		})
	}
}
