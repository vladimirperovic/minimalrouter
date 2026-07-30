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

func benchmarkMux(b *testing.B) (*http.ServeMux, func()) {
	b.Helper()
	tempDir, err := os.MkdirTemp("", "router-benchmark-*")
	if err != nil {
		b.Fatal(err)
	}
	store, err := config.NewStore(tempDir)
	if err != nil {
		os.RemoveAll(tempDir)
		b.Fatal(err)
	}
	engine := apply.NewEngineWithClient(config.DefaultConfig(), store, apiTestApplyClient{})
	server := NewServer(engine)
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	return mux, func() { _ = os.RemoveAll(tempDir) }
}

func BenchmarkAPISetupStatusParallel(b *testing.B) {
	mux, cleanup := benchmarkMux(b)
	defer cleanup()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				b.Fatalf("unexpected status: %d", recorder.Code)
			}
		}
	})
}

func BenchmarkAPIProtectedEndpointParallel(b *testing.B) {
	mux, cleanup := benchmarkMux(b)
	defer cleanup()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/system", nil)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusUnauthorized {
				b.Fatalf("unexpected status: %d", recorder.Code)
			}
		}
	})
}

func BenchmarkAPIMalformedLoginParallel(b *testing.B) {
	mux, cleanup := benchmarkMux(b)
	defer cleanup()
	body := []byte(`{"password":`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, req)
			if recorder.Code == http.StatusInternalServerError {
				b.Fatalf("malformed login caused internal error: %s", recorder.Body.String())
			}
		}
	})
}
