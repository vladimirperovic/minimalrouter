package api

import (
	"bytes"
	"io"
	"log"
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
	previousLogOutput := log.Writer()
	log.SetOutput(io.Discard)
	return mux, func() {
		log.SetOutput(previousLogOutput)
		_ = os.RemoveAll(tempDir)
	}
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

<<<<<<< HEAD
func BenchmarkAPIMalformedRequestParallel(b *testing.B) {
	mux, cleanup := benchmarkMux(b)
	defer cleanup()
	body := []byte(`{`)
=======
func BenchmarkAPIMalformedLoginParallel(b *testing.B) {
	mux, cleanup := benchmarkMux(b)
	defer cleanup()
	body := []byte(`{"password":`)
>>>>>>> public/main
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, req)
			if recorder.Code == http.StatusInternalServerError {
<<<<<<< HEAD
				b.Fatalf("malformed request caused internal error: %s", recorder.Body.String())
=======
				b.Fatalf("malformed login caused internal error: %s", recorder.Body.String())
>>>>>>> public/main
			}
		}
	})
}
