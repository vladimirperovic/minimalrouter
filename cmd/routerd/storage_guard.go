package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/storage"
)

func storagePressureHandler(dataDir string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mutationNeedsDurableWrite(r.Method, r.URL.Path) {
			status := storage.Inspect(dataDir)
			if status.Available && !status.DurableWritesAllowed {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusInsufficientStorage)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error":            "Storage pressure prevents durable configuration changes. Routing remains active; free disk space and retry.",
					"storage_pressure": status.Level,
				})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func mutationNeedsDurableWrite(method, path string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}

	// POST endpoints below are read-like, memory-only, or are specifically
	// needed to recover/export state while the appliance is under pressure.
	if path == "/api/v1/auth/logout" ||
		path == "/api/v1/backup/export" ||
		path == "/api/v1/firmware/verify" ||
		path == "/api/v1/recovery/reconcile" ||
		path == "/api/v1/auth/totp/enroll" ||
		strings.HasSuffix(path, "/preview") {
		return false
	}
	return strings.HasPrefix(path, "/api/")
}
