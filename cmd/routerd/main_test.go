package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestManagementDestinationRejectsWANAddress(t *testing.T) {
	cfg := config.DefaultConfig()
	engine := apply.NewEngine(cfg, nil)
	handler := managementDestinationHandler(engine, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	cases := []struct {
		name        string
		destination string
		want        int
	}{
		{name: "LAN accepted", destination: "192.168.1.1:8443", want: http.StatusNoContent},
		{name: "WAN rejected", destination: "203.0.113.20:8443", want: http.StatusNotFound},
		{name: "loopback rejected in appliance mode", destination: "127.0.0.1:8443", want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://192.168.1.1:8443/", nil)
			ctx := context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{
				IP:   net.ParseIP(tc.destination[:len(tc.destination)-5]),
				Port: 8443,
			})
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("destination %s returned %d, want %d", tc.destination, rec.Code, tc.want)
			}
		})
	}
}

func TestManagementDestinationRejectsDNSRebindingHost(t *testing.T) {
	cfg := config.DefaultConfig()
	engine := apply.NewEngine(cfg, nil)
	handler := managementDestinationHandler(engine, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "https://attacker.example:8443/", nil)
	req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{
		IP: net.ParseIP(cfg.LAN.IPAddress), Port: 8443,
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("DNS-rebinding Host was accepted: %d", recorder.Code)
	}
}

func TestWaitForPrivilegedHelperSurvivesDelayedRestart(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "apply.sock")
	ready := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			t.Errorf("start delayed helper: %v", err)
			close(ready)
			return
		}
		defer listener.Close()
		close(ready)
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := waitForPrivilegedHelper(ctx, socketPath); err != nil {
		t.Fatalf("delayed privileged helper was rejected: %v", err)
	}
	<-ready
}

func TestWaitForPrivilegedHelperRemainsBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := waitForPrivilegedHelper(ctx, filepath.Join(t.TempDir(), "missing.sock")); err != context.DeadlineExceeded {
		t.Fatalf("unavailable helper returned %v, want bounded deadline", err)
	}
}
