package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
)

func currentTOTPCode(t *testing.T, secret string) string {
	t.Helper()
	decoded, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	counter := time.Now().Unix() / auth.TOTPPeriod
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))
	mac := hmac.New(sha1.New, decoded)
	_, _ = mac.Write(buf)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}

func TestTOTPDisableDecodesPasswordBeforeVerification(t *testing.T) {
	server, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	server.store = server.engine.GetStore()
	const password = "correct-admin-password-123!"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.SetAdminHash(hash); err != nil {
		t.Fatal(err)
	}
	server.adminHash = hash

	const secret = "JBSWY3DPEHPK3PXP"
	if err := server.store.SetAdminTOTPSecret(secret); err != nil {
		t.Fatal(err)
	}

	session := server.sessionMgr.CreateSession()
	body, err := json.Marshal(map[string]string{
		"current_password": password,
		"code":             currentTOTPCode(t, secret),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "https://192.168.1.1/api/v1/auth/totp/disable", bytes.NewReader(body))
	req.Host = "192.168.1.1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.CSRFHeaderName, session.CSRFToken)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("TOTP disable returned %d: %s", recorder.Code, recorder.Body.String())
	}
	stored, err := server.store.GetAdminTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if stored != "" {
		t.Fatal("TOTP secret remained configured after successful disable")
	}
}
