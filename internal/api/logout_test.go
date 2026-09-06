package api

import (
	"errors"
	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"net/http"
	"net/http/httptest"
	"testing"
)

type unavailableRevocation struct{ *auth.SessionManager }

func (m unavailableRevocation) DestroySession(*http.Request, http.ResponseWriter) error {
	return errors.New("test persistence failure")
}

func TestLogoutReportsRevocationFailureAndAllowsRetry(t *testing.T) {
	manager := auth.NewSessionManager()
	session := manager.CreateSession()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	server := &Server{sessionMgr: unavailableRevocation{manager}}
	failed := httptest.NewRecorder()
	server.handleLogout(failed, request)
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("revocation failure reported %d", failed.Code)
	}
	if len(failed.Result().Cookies()) != 0 {
		t.Fatal("failed logout discarded retry cookie")
	}
	if _, err := manager.ValidateSession(request); err != nil {
		t.Fatal("fixture lost session before retry")
	}
	server.sessionMgr = manager
	retried := httptest.NewRecorder()
	server.handleLogout(retried, request)
	if retried.Code != http.StatusOK {
		t.Fatalf("retry returned %d", retried.Code)
	}
	if _, err := manager.ValidateSession(request); err == nil {
		t.Fatal("successful retry left session valid")
	}
	cookies := retried.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatal("successful logout did not expire cookie")
	}
}
