package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDestroySessionRevokesServerState(t *testing.T) {
	for _, withWriter := range []bool{true, false} {
		t.Run(map[bool]string{true: "cookie", false: "nil-writer"}[withWriter], func(t *testing.T) {
			manager := NewSessionManager()
			session := manager.CreateSession()
			if session == nil {
				t.Fatal("create session")
			}
			request := httptest.NewRequest(http.MethodPost, "/logout", nil)
			request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
			recorder := httptest.NewRecorder()
			var writer http.ResponseWriter
			if withWriter {
				writer = recorder
			}
			if err := manager.DestroySession(request, writer); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.ValidateSession(request); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("revoked cookie accepted: %v", err)
			}
			if withWriter {
				cookies := recorder.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Name != SessionCookieName || cookies[0].Value != "" || cookies[0].MaxAge != -1 || cookies[0].Path != "/" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
					t.Fatalf("missing secure expired cookie: %v", cookies)
				}
			}
			if err := manager.DestroySession(request, nil); err != nil {
				t.Fatalf("repeated revocation: %v", err)
			}
		})
	}
}
