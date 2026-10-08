package api

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
	"github.com/vladimirperovic/minimalrouter/internal/recoverybackup"
)

type apiFilterHelper struct{}

func (apiFilterHelper) Call(context.Context, dnsfilter.Request, func(io.Writer) error) (dnsfilter.Applied, error) {
	return dnsfilter.Applied{Policy: dnsfilter.DefaultPolicy()}, nil
}

func TestDNSFilterRoutesRequireTrustedWriteSessionAndCSRF(t *testing.T) {
	s, handler, _, session := setupDNSActivityServer(t, true)
	filter, err := dnsfilter.Open(t.TempDir(), apiFilterHelper{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer filter.Close()
	s.ConfigureDNSFilter(filter)
	readonly := s.sessionMgr.CreateSessionWithMode(true)
	for _, route := range []struct{ method, path string }{{"GET", "/api/v1/dns-filter"}, {"PUT", "/api/v1/dns-filter"}, {"POST", "/api/v1/dns-filter/refresh"}, {"POST", "/api/v1/dns-filter/check"}} {
		if got := regressionRequest(t, handler, route.method, route.path, "192.168.1.10:1234", nil, nil); got.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", route.path, got.Code)
		}
		if got := regressionRequest(t, handler, route.method, route.path, "192.168.2.10:1234", nil, session); got.Code != 403 {
			t.Fatalf("untrusted %s: %d", route.path, got.Code)
		}
		if route.method != "GET" {
			if got := regressionRequest(t, handler, route.method, route.path, "192.168.1.10:1234", nil, readonly); got.Code != 403 {
				t.Fatalf("readonly %s: %d", route.path, got.Code)
			}
			r := httptest.NewRequest(route.method, route.path, nil)
			r.RemoteAddr = "192.168.1.10:1234"
			r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal("mutation without CSRF accepted")
			}
		}
	}
	got := regressionRequest(t, handler, "GET", "/api/v1/dns-filter", "192.168.1.10:1234", nil, readonly)
	if got.Code != 200 || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status: %d %s", got.Code, got.Body.String())
	}
	got = regressionRequest(t, handler, "PUT", "/api/v1/dns-filter", "192.168.1.10:1234", map[string]any{"categories": map[string]bool{"unknown": true}}, session)
	if got.Code != 422 {
		t.Fatal("unknown category accepted")
	}
}

func TestBackupExportUsesTwelveCharacterDashboardPasswordAndImportsOldFiles(t *testing.T) {
	s, handler := storeBackedTestServer(t)
	const password = "Abcd1234!?xy"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.SetAdminHash(hash); err != nil {
		t.Fatal(err)
	}
	session := regressionLogin(t, handler, "192.168.1.10:1234", password)
	response := regressionRequest(t, handler, "POST", "/api/v1/backup/export", "192.168.1.10:1234", map[string]string{"current_password": password}, session)
	if response.Code != 200 {
		t.Fatalf("export: %d %s", response.Code, response.Body.String())
	}
	if _, err = recoverybackup.Decrypt(response.Body.Bytes(), password); err != nil {
		t.Fatal(err)
	}
	stale := regressionRequest(t, handler, "POST", "/api/v1/backup/export", "192.168.1.10:1234", map[string]string{"current_password": password, "backup_passphrase": "different-old-ui-password"}, session)
	if stale.Code != 422 {
		t.Fatal("stale UI password mismatch silently accepted")
	}
	legacy, err := config.EncryptConfigBackup(s.engine.GetCurrentConfig(), "old separate password")
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		data []byte
		old  string
	}{{response.Body.Bytes(), ""}, {legacy, "old separate password"}} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("current_password", password)
		if sample.old != "" {
			_ = writer.WriteField("backup_passphrase", sample.old)
		}
		file, err := writer.CreateFormFile("backup", "test.mrbak")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write(sample.data)
		_ = writer.Close()
		r := httptest.NewRequest("POST", "/api/v1/backup/import/preview", &body)
		r.RemoteAddr = "192.168.1.10:1234"
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set(auth.CSRFHeaderName, session.CSRFToken)
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("import: %d %s", w.Code, w.Body.String())
		}
	}
}
