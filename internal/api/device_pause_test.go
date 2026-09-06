package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
)

func TestDevicePauseErrorHTTPStatuses(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		status   int
		recovery bool
	}{
		{"invalid", &apply.ActionError{Code: apply.ActionInvalid, Message: "invalid IP"}, 400, false},
		{"conflict", &apply.ActionError{Code: apply.ActionConflict, Message: "confirmation pending"}, 409, false},
		{"failed rollback completed", &apply.ActionError{Code: apply.ActionFailed, Message: "action failed; restored"}, 500, false},
		{"helper unavailable", &apply.ActionError{Code: apply.ActionUnavailable, Message: "helper unavailable"}, 503, false},
		{"recovery", fmt.Errorf("wrapped: %w", &apply.ActionError{Code: apply.ActionRecoveryRequired, Message: "rollback failed"}), 503, true},
		{"transport", errors.New("internal path and detail"), 503, false},
		{"timeout", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), 504, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeDevicePauseError(w, test.err)
			if w.Code != test.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var body struct {
				Error    string `json:"error"`
				Code     string `json:"code"`
				Recovery bool   `json:"recovery_required"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code == "" || body.Error == "" || body.Recovery != test.recovery {
				t.Fatalf("body=%+v", body)
			}
			if strings.Contains(body.Error, "internal path") {
				t.Fatal("transport detail leaked")
			}
		})
	}
}

func TestDevicePauseHandlersRejectInvalidInputWith400(t *testing.T) {
	server, _, _, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	for _, test := range []struct {
		name, body string
		resume     bool
	}{
		{"malformed", `{`, false}, {"bad IP", `{"ip":"bad"}`, false}, {"outside LAN", `{"ip":"203.0.113.10"}`, false},
		{"router IP", `{"ip":"192.168.1.1"}`, false}, {"duration", `{"ip":"192.168.1.10","seconds":1}`, false},
		{"resume duration", `{"ip":"192.168.1.10","seconds":900}`, true}, {"resume IP", `{"ip":"bad"}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			w := httptest.NewRecorder()
			if test.resume {
				server.handleResumeDevice(w, req)
			} else {
				server.handlePauseDevice(w, req)
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDevicePauseOversizedRequestReturns413(t *testing.T) {
	server, _, _, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	for _, resume := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ip":"`+strings.Repeat("a", 1<<20)+`"}`))
		w := httptest.NewRecorder()
		if resume {
			server.handleResumeDevice(w, req)
		} else {
			server.handlePauseDevice(w, req)
		}
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
