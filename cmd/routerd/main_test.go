package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

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
			req := httptest.NewRequest(http.MethodGet, "https://router.test/", nil)
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
