package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func FuzzMalformedUnauthenticatedRequests(f *testing.F) {
	tempDir, err := os.MkdirTemp("", "router-fuzz-*")
	if err != nil {
		f.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := config.NewStore(tempDir)
	if err != nil {
		f.Fatal(err)
	}
	engine := apply.NewEngineWithClient(config.DefaultConfig(), store, apiTestApplyClient{})
	server := NewServer(engine)
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	for _, seed := range [][]byte{
		{},
		[]byte(`{}`),
		[]byte(`{"password":"wrong"}`),
		[]byte(`{"password":`),
		bytes.Repeat([]byte("A"), 4096),
	} {
		f.Add(seed, uint8(0), uint8(0))
	}

	endpoints := []string{
		"/api/v1/auth/login",
		"/api/v1/config",
		"/api/v1/system",
		"/api/v1/snapshots",
		"/api/v1/system/diagnostics",
		"/api/v1/system/update/install",
		"/api/v1/backup/decrypt",
		"/api/v1/setup/status",
	}
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

	f.Fuzz(func(t *testing.T, body []byte, endpointIndex, methodIndex uint8) {
		if len(body) > 64*1024 {
			t.Skip()
		}
		endpoint := endpoints[int(endpointIndex)%len(endpoints)]
		method := methods[int(methodIndex)%len(methods)]
		req := httptest.NewRequest(method, endpoint, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://attacker.invalid")
		recorder := httptest.NewRecorder()

		mux.ServeHTTP(recorder, req)

		if recorder.Code < 100 || recorder.Code > 599 {
			t.Fatalf("invalid HTTP status %d for %s %s", recorder.Code, method, endpoint)
		}
		// A fresh appliance intentionally returns 503 until the setup wizard has
		// completed. Panics and generic internal errors remain test failures.
		if recorder.Code == http.StatusInternalServerError {
			t.Fatalf("malformed unauthenticated request caused %d for %s %s: %s", recorder.Code, method, endpoint, recorder.Body.String())
		}
		if recorder.Body.Len() > 1024*1024 {
			t.Fatalf("response exceeded 1 MiB for %s %s", method, endpoint)
		}
	})
}
