package persistent

import (
	"net/http/httptest"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestPreviewCookieModeIsExplicit(t *testing.T) {
	store, err := config.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	manager := NewPersistentSessionManagerWithSecureCookies(store, false)
	recorder := httptest.NewRecorder()
	manager.SetSessionCookie(recorder, &auth.Session{ID: "preview-session"})
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Secure {
		t.Fatal("explicit preview cookie should be loopback-HTTP compatible")
	}

	production := NewPersistentSessionManager(store)
	recorder = httptest.NewRecorder()
	production.SetSessionCookie(recorder, &auth.Session{ID: "production-session"})
	if cookies = recorder.Result().Cookies(); len(cookies) != 1 || !cookies[0].Secure {
		t.Fatal("production cookie must remain Secure by default")
	}
}
