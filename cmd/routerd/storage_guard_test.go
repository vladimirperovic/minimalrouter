package main

import (
	"net/http"
	"testing"
)

func TestMutationNeedsDurableWrite(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/api/v1/config", false},
		{http.MethodPut, "/api/v1/config", true},
		{http.MethodPost, "/api/v1/snapshots", true},
		{http.MethodPost, "/api/v1/transactions/tx/confirm", true},
		{http.MethodPost, "/api/v1/gateway/settings", true},
		{http.MethodPost, "/api/v1/backup/export", false},
		{http.MethodPost, "/api/v1/import/pfsense/preview", false},
		{http.MethodPost, "/api/v1/recovery/reconcile", false},
		{http.MethodPost, "/api/v1/firmware/verify", false},
		{http.MethodPost, "/not-api", false},
	}
	for _, tt := range tests {
		if got := mutationNeedsDurableWrite(tt.method, tt.path); got != tt.want {
			t.Errorf("%s %s = %v, want %v", tt.method, tt.path, got, tt.want)
		}
	}
}
