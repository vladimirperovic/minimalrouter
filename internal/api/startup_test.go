package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/startup"
)

func TestStartupSummariesAndSelectedSamplesAreReadOnlyAndProtected(t *testing.T) {
	server, mux, handler, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	dataDir := t.TempDir()
	startupDir := filepath.Join(dataDir, "startup")
	if err := os.MkdirAll(startupDir, 0700); err != nil {
		t.Fatal(err)
	}
	boot := startup.Boot{ID: "test-boot", StartedAt: time.Now().UTC(), Completed: true, CompletionReason: "timeout", Samples: []startup.Sample{{OffsetSeconds: 1, CPUPercent: 20}}}
	data, _ := json.Marshal(boot)
	if err := os.WriteFile(filepath.Join(startupDir, "test-boot.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	server.RegisterStartupRoutes(mux, dataDir)
	session := server.sessionMgr.CreateSessionWithMode(true)
	for _, path := range []string{"/api/v1/startup/boots", "/api/v1/startup/boots/test-boot"} {
		for _, authed := range []bool{false, true} {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			if authed {
				r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if !authed {
				if w.Code != 401 {
					t.Fatalf("unauthed %s: %d", path, w.Code)
				}
				continue
			}
			if w.Code != 200 {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if path == "/api/v1/startup/boots" {
				var summaries []map[string]json.RawMessage
				_ = json.Unmarshal(payload["boots"], &summaries)
				if _, exists := summaries[0]["samples"]; exists {
					t.Fatal("summary included full samples")
				}
				if string(summaries[0]["status"]) != `"timeout"` || string(summaries[0]["sample_count"]) != "1" {
					t.Fatalf("bad summary: %s", w.Body.String())
				}
			} else {
				var result startup.Boot
				_ = json.Unmarshal(payload["boot"], &result)
				if len(result.Samples) != 1 || result.Events == nil {
					t.Fatal("missing samples or null events")
				}
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("startup cache policy missing")
			}
		}
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = "203.0.113.9:1234"
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("untrusted read accepted: %d", w.Code)
		}
	}
}
