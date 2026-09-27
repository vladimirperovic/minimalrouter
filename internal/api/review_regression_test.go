package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func regressionRequest(t *testing.T, handler http.Handler, method, path, remote string, body any, session *auth.Session) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, "https://192.168.1.1"+path, reader)
	req.Host = "192.168.1.1"
	if remote != "" {
		req.RemoteAddr = remote
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if session != nil {
		req.Header.Set(auth.CSRFHeaderName, session.CSRFToken)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func regressionLogin(t *testing.T, handler http.Handler, remote, password string) *auth.Session {
	t.Helper()
	response := regressionRequest(t, handler, http.MethodPost, "/api/v1/auth/login", remote, map[string]string{"password": password}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == auth.SessionCookieName {
			return &auth.Session{ID: cookie.Value, CSRFToken: body.CSRF}
		}
	}
	t.Fatal("login did not set a session cookie")
	return nil
}

func storeBackedTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	server, _, handler, dir := setupTestServer(t)
	t.Cleanup(func() { os.RemoveAll(dir) })
	server.store = server.engine.GetStore()
	return server, handler
}

// The local recovery console resets credentials in SQLite while routerd keeps
// running. Every re-authentication must follow the store, like login does.
func TestReauthenticationFollowsRecoveryCredentialReset(t *testing.T) {
	server, handler := storeBackedTestServer(t)
	const oldPassword = "old-admin-password-123!"
	const newPassword = "new-admin-password-456!"
	oldHash, err := auth.HashPassword(oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.SetAdminHash(oldHash); err != nil {
		t.Fatal(err)
	}
	server.adminHash = oldHash // what routerd cached at startup

	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.RecoveryResetAuthentication(newHash, true); err != nil {
		t.Fatal(err)
	}

	session := regressionLogin(t, handler, "192.168.1.10:1000", newPassword)
	export := func(password string) int {
		return regressionRequest(t, handler, http.MethodPost, "/api/v1/backup/export", "192.168.1.10:1001",
			map[string]string{"current_password": password, "backup_passphrase": "backup-passphrase-123456"}, session).Code
	}
	if code := export(oldPassword); code != http.StatusUnauthorized {
		t.Fatalf("revoked password still confirmed a backup export: %d", code)
	}
	if code := export(newPassword); code != http.StatusOK {
		t.Fatalf("current password was refused for a backup export: %d", code)
	}

	change := regressionRequest(t, handler, http.MethodPost, "/api/v1/auth/change-password", "192.168.1.10:1002",
		map[string]string{"old_password": newPassword, "new_password": "third-admin-password-789!"}, session)
	if change.Code != http.StatusOK {
		t.Fatalf("password change with the current password failed: %d %s", change.Code, change.Body.String())
	}
}

func TestChangePasswordAlwaysRequiresTheCurrentPassword(t *testing.T) {
	server, _, handler, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	// No administrator credential is stored: an existing session must still
	// not be able to set one without proof.
	session := server.sessionMgr.CreateSession()
	response := regressionRequest(t, handler, http.MethodPost, "/api/v1/auth/change-password", "",
		map[string]string{"old_password": "", "new_password": "attacker-chosen-password-1"}, session)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("password was set without the current password: %d %s", response.Code, response.Body.String())
	}
	if server.adminHash != "" {
		t.Fatal("administrator credential changed")
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	server, handler := storeBackedTestServer(t)
	const password = "correct-admin-password-123!"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.SetAdminHash(hash); err != nil {
		t.Fatal(err)
	}
	if err := server.store.SetAdminTOTPSecret("JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}

	cases := []map[string]string{
		{"password": "wrong-password-xyz-000", "totp_code": "000000"},
		{"password": password, "totp_code": "000000"},
		{"password": password},
	}
	var reference string
	for i, body := range cases {
		response := regressionRequest(t, handler, http.MethodPost, "/api/v1/auth/login",
			fmt.Sprintf("192.168.1.%d:1", 20+i), body, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("case %d: status %d", i, response.Code)
		}
		got := strings.TrimSpace(response.Body.String())
		if reference == "" {
			reference = got
			continue
		}
		if got != reference {
			t.Fatalf("case %d response %q differs from %q: it reveals which factor was wrong", i, got, reference)
		}
	}
}

// A credential committed outside this process (console setup or recovery)
// must close the unauthenticated first-run surface immediately.
func TestSetupSurfaceFollowsCanonicalCredential(t *testing.T) {
	server, handler := storeBackedTestServer(t)
	hash, err := auth.HashPassword("console-admin-password-123!")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.RecoveryResetAuthentication(hash, false); err != nil {
		t.Fatal(err)
	}

	status := regressionRequest(t, handler, http.MethodGet, "/api/v1/setup/status", "", nil, nil)
	if !strings.Contains(status.Body.String(), `"is_configured":true`) || strings.Contains(status.Body.String(), "lan_interface") {
		t.Fatalf("setup status still exposes first-run details: %s", status.Body.String())
	}
	if code := regressionRequest(t, handler, http.MethodGet, "/api/v1/setup/interfaces", "", nil, nil).Code; code != http.StatusNotFound {
		t.Fatalf("setup interface discovery still open: %d", code)
	}
	apply := regressionRequest(t, handler, http.MethodPost, "/api/v1/setup/apply", "", map[string]string{
		"admin_password": "someone-elses-password-1",
	}, nil)
	if apply.Code != http.StatusForbidden {
		t.Fatalf("setup wizard accepted a re-run: %d %s", apply.Code, apply.Body.String())
	}
}

func TestSetupWizardAfterRecoveryConsoleRevision(t *testing.T) {
	dir := t.TempDir()
	store, err := config.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	latest, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	// e.g. `router-recovery set-wan` before first-run setup
	latest.WAN.Interface = "enp1s0"
	latest.Revision++
	if err := store.SaveConfig(latest); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	engine := apply.NewEngineWithClient(current, store, apiTestApplyClient{})
	server := NewServer(engine)
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	response := regressionRequest(t, trustedMux(mux), http.MethodPost, "/api/v1/setup/apply", "", map[string]string{
		"wan_interface":  "enp1s0",
		"admin_password": "placeholder-admin-password-123!",
		"lan_interface":  current.LAN.Interface,
		"lan_ip_address": current.LAN.IPAddress,
	}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("wizard failed after a recovery revision: %d %s", response.Code, response.Body.String())
	}
	if got := server.engine.GetCurrentConfig().Revision; got != current.Revision+1 {
		t.Fatalf("setup revision = %d, want %d", got, current.Revision+1)
	}
}

type revokedAfterMiddleware struct {
	SessionManagerInterface
	calls atomic.Int32
}

func (m *revokedAfterMiddleware) ValidateSession(r *http.Request) (*auth.Session, error) {
	if m.calls.Add(1) > 1 {
		return nil, auth.ErrUnauthorized
	}
	return m.SessionManagerInterface.ValidateSession(r)
}

// A session revoked between authMiddleware and the handler (a password change
// in another tab) must not crash the handler with a nil session.
func TestHandlersUseTheMiddlewareSession(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		body         any
		want         int
	}{
		{http.MethodGet, "/api/v1/auth/session", nil, http.StatusOK},
		{http.MethodPost, "/api/v1/auth/totp/enable", map[string]string{"code": "000000"}, http.StatusUnauthorized},
	} {
		server, _, handler, dir := setupTestServer(t)
		session := server.sessionMgr.CreateSession()
		server.sessionMgr = &revokedAfterMiddleware{SessionManagerInterface: server.sessionMgr}
		response := regressionRequest(t, handler, tc.method, tc.path, "", tc.body, session)
		os.RemoveAll(dir)
		if response.Code != tc.want {
			t.Fatalf("%s %s: status %d, want %d: %s", tc.method, tc.path, response.Code, tc.want, response.Body.String())
		}
	}
}

func countAuditEvents(t *testing.T, store *config.SQLiteStore) map[string]int {
	t.Helper()
	events, err := store.ListAuditEvents(500)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.EventType]++
	}
	return counts
}

func TestUnauthenticatedRequestsCannotFloodTheAuditLog(t *testing.T) {
	server, handler := storeBackedTestServer(t)
	for i := 0; i < 50; i++ {
		regressionRequest(t, handler, http.MethodGet, "/api/v1/system", "192.168.1.66:4000", nil, nil)
	}
	counts := countAuditEvents(t, server.store)
	if counts["auth.unauthorized"] != auditThrottlePerSource {
		t.Fatalf("recorded %d unauthorized events, want %d", counts["auth.unauthorized"], auditThrottlePerSource)
	}
	if counts["audit.throttled"] != 1 {
		t.Fatalf("suppression marker count = %d, want 1", counts["audit.throttled"])
	}
}

func TestAuditThrottleReportsSuppressedCount(t *testing.T) {
	var throttle auditThrottle
	start := time.Unix(1_800_000_000, 0)
	recorded := 0
	marks := 0
	for i := 0; i < auditThrottlePerSource+7; i++ {
		decision := throttle.admit("auth.unauthorized\x00192.168.1.2", start.Add(time.Duration(i)*time.Millisecond))
		if decision.record {
			recorded++
		}
		if decision.markSuppression {
			marks++
		}
	}
	if recorded != auditThrottlePerSource || marks != 1 {
		t.Fatalf("recorded=%d marks=%d", recorded, marks)
	}
	next := throttle.admit("auth.unauthorized\x00192.168.1.2", start.Add(auditThrottleWindow))
	if !next.record || next.previousSuppressed != 7 || !next.previousWindowStart.Equal(start) {
		t.Fatalf("next window decision = %+v", next)
	}

	// The global cap applies across sources, and the source table is bounded.
	var global auditThrottle
	admitted := 0
	for i := 0; i < auditThrottleGlobal+50; i++ {
		if global.admit("auth.unauthorized\x00source-"+strconv.Itoa(i), start).record {
			admitted++
		}
	}
	if admitted != auditThrottleGlobal {
		t.Fatalf("global cap admitted %d, want %d", admitted, auditThrottleGlobal)
	}
}

func TestWakeOnLANRequiresEthernetAddress(t *testing.T) {
	server, _, handler, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	session := server.sessionMgr.CreateSession()
	response := regressionRequest(t, handler, http.MethodPost, "/api/v1/network/wol", "",
		map[string]string{"mac": "00:11:22:33:44:55:66:77"}, session)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("EUI-64 address accepted for Wake-on-LAN: %d", response.Code)
	}
}

func TestManualSnapshotSurvivesAutomaticSnapshots(t *testing.T) {
	server, _, handler, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	session := server.sessionMgr.CreateSession()
	created := regressionRequest(t, handler, http.MethodPost, "/api/v1/snapshots", "", nil, session)
	if created.Code != http.StatusOK {
		t.Fatalf("manual snapshot failed: %d", created.Code)
	}
	var body struct {
		Snapshot config.Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	store := server.engine.GetStore()
	for i := 0; i < 25; i++ {
		if _, err := store.CreateSnapshot(server.engine.GetCurrentConfig()); err != nil {
			t.Fatal(err)
		}
	}
	list := regressionRequest(t, handler, http.MethodGet, "/api/v1/snapshots", "", nil, session)
	if !strings.Contains(list.Body.String(), body.Snapshot.ID) || !strings.Contains(list.Body.String(), `"kind":"manual"`) {
		t.Fatalf("manual snapshot was pruned by automatic ones: %s", list.Body.String())
	}
}

func TestRedactedPresharedKeyNeverMovesBetweenPeers(t *testing.T) {
	const (
		keyA = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
		keyB = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="
		pskA = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCA="
		pskB = "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDA="
	)
	current := config.DefaultConfig()
	current.WireGuard.Peers = []config.WireGuardPeer{
		{ID: "wg-dup", PublicKey: keyA, PresharedKey: pskA},
		{ID: "wg-dup", PublicKey: keyB, PresharedKey: pskB},
		{ID: "wg-a", PublicKey: keyA, PresharedKey: pskA},
	}
	candidate := current.DeepCopy()
	for i := range candidate.WireGuard.Peers {
		candidate.WireGuard.Peers[i].PresharedKey = redactedSecret
	}
	// A re-keyed peer must not inherit the previous key pair's secret.
	candidate.WireGuard.Peers[2].PublicKey = keyB

	restored := restoreRedactedConfig(candidate, current)
	if got := restored.WireGuard.Peers[1].PresharedKey; got != pskB && got != redactedSecret {
		t.Fatalf("peer B received another peer's preshared key: %q", got)
	}
	if got := restored.WireGuard.Peers[0].PresharedKey; got != pskA && got != redactedSecret {
		t.Fatalf("peer A received another peer's preshared key: %q", got)
	}
	if got := restored.WireGuard.Peers[2].PresharedKey; got != redactedSecret {
		t.Fatalf("re-keyed peer inherited a preshared key: %q", got)
	}

	unique := current.DeepCopy()
	unique.WireGuard.Peers = unique.WireGuard.Peers[2:]
	uniqueCandidate := unique.DeepCopy()
	uniqueCandidate.WireGuard.Peers[0].PresharedKey = redactedSecret
	if got := restoreRedactedConfig(uniqueCandidate, unique).WireGuard.Peers[0].PresharedKey; got != pskA {
		t.Fatalf("unchanged unique peer lost its preshared key: %q", got)
	}

	recorder := httptest.NewRecorder()
	if _, ok := findWireGuardPeer(recorder, current.WireGuard.Peers, "wg-dup"); ok || recorder.Code != http.StatusConflict {
		t.Fatalf("ambiguous peer identifier resolved: ok=%v status=%d", ok, recorder.Code)
	}
}

// The server-wide ReadTimeout covers the whole body; an authenticated upload
// handler must be able to extend it through the audit response wrapper.
func TestUploadReadDeadlineCanBeExtended(t *testing.T) {
	for _, extend := range []bool{false, true} {
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped := &auditResponseWriter{ResponseWriter: w}
			if extend {
				extendUploadReadDeadline(wrapped, 5*time.Second)
			}
			if _, err := io.ReadAll(r.Body); err != nil {
				http.Error(wrapped, "body read failed", http.StatusRequestTimeout)
				return
			}
			wrapped.WriteHeader(http.StatusNoContent)
		}))
		server.Config.ReadTimeout = 300 * time.Millisecond
		server.Start()

		reader, writer := io.Pipe()
		go func() {
			for i := 0; i < 8; i++ {
				_, _ = writer.Write(bytes.Repeat([]byte("x"), 1024))
				time.Sleep(100 * time.Millisecond)
			}
			_ = writer.Close()
		}()
		req, _ := http.NewRequest(http.MethodPost, server.URL, reader)
		resp, err := http.DefaultClient.Do(req)
		status := 0
		if err == nil {
			status = resp.StatusCode
			resp.Body.Close()
		}
		server.Close()
		if extend && status != http.StatusNoContent {
			t.Fatalf("extended upload failed: status=%d err=%v", status, err)
		}
		if !extend && status == http.StatusNoContent {
			t.Fatal("control upload unexpectedly outlived the server ReadTimeout")
		}
	}
}

func withSpeedtestHost(t *testing.T, handler http.Handler) {
	t.Helper()
	server := httptest.NewServer(handler)
	previous := speedtestHost
	speedtestHost = server.URL
	t.Cleanup(func() {
		speedtestHost = previous
		server.Close()
	})
}

func TestSpeedtestMeasuresSlowLinesWithinTheBudget(t *testing.T) {
	withSpeedtestHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__down":
			chunk := bytes.Repeat([]byte("d"), 64<<10)
			flusher := w.(http.Flusher)
			for {
				if _, err := w.Write(chunk); err != nil {
					return
				}
				flusher.Flush()
				select {
				case <-r.Context().Done():
					return
				case <-time.After(20 * time.Millisecond):
				}
			}
		case "/__up":
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		}
	}))
	client := newSpeedtestClient()
	defer client.CloseIdleConnections()

	// A 50 MB sample at ~3 MB/s would need 16 s; the phase stops at the budget.
	started := time.Now()
	down, err := measureDownload(context.Background(), client, 50<<20, 400*time.Millisecond)
	if err != nil {
		t.Fatalf("budget-limited download failed: %v", err)
	}
	if down <= 0 || time.Since(started) > 5*time.Second {
		t.Fatalf("download = %.2f Mbit/s after %s", down, time.Since(started))
	}
	up, err := measureUpload(context.Background(), client, 4<<20, 200*time.Millisecond)
	if err != nil || up <= 0 {
		t.Fatalf("upload = %.2f, err=%v", up, err)
	}
}

func TestSpeedtestRejectsTruncatedAndUnreadSamples(t *testing.T) {
	withSpeedtestHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__down":
			_, _ = w.Write([]byte("short error page"))
		case "/__up":
			// Answers without consuming the request body.
			w.WriteHeader(http.StatusOK)
		}
	}))
	client := newSpeedtestClient()
	defer client.CloseIdleConnections()
	if _, err := measureDownload(context.Background(), client, 8<<20, 5*time.Second); err == nil {
		t.Fatal("truncated download was reported as a speed")
	}
	// Far larger than any socket buffer, so an early answer cannot look like a
	// completed transfer.
	if _, err := uploadOnce(context.Background(), client, 32<<20, 5*time.Second); err == nil {
		t.Fatal("unread upload was reported as a speed")
	}
}
