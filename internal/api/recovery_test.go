package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
)

func recoveryTestPreview(t *testing.T, s *Server, session *auth.Session, cfg config.SystemConfig, source string, policy *dnsfilter.Policy) string {
	t.Helper()
	r := httptest.NewRequest("POST", "/preview", nil)
	r.RemoteAddr = "192.168.1.10:1234"
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	w := httptest.NewRecorder()
	s.recoveryPreview(w, r, cfg, source, policy, nil)
	if w.Code != 200 {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		ID         string             `json:"import_id"`
		Base       config.Revision    `json:"base_revision"`
		Assessment recoveryAssessment `json:"assessment"`
		Policy     *dnsfilter.Policy  `json:"dns_filter"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID == "" || response.Base != cfg.Revision {
		t.Fatal("missing preview revision/token")
	}
	if policy != nil && (response.Policy.Categories == nil || response.Policy.Exceptions == nil) {
		t.Fatal("nullable DNS collections escaped normalization")
	}
	return response.ID
}

func TestRecoveryPreviewsRejectStaleRevisionAndDoNotConsumeOtherSessions(t *testing.T) {
	for _, source := range []string{"backup", "pfsense"} {
		t.Run(source, func(t *testing.T) {
			s, h := storeBackedTestServer(t)
			session := s.sessionMgr.CreateSession()
			other := s.sessionMgr.CreateSession()
			cfg := s.engine.GetCurrentConfig()
			cfg.System.Hostname = "restored-router"
			id := recoveryTestPreview(t, s, session, cfg, source, nil)
			path := "/api/v1/import/" + source + "/" + id + "/apply"
			if w := regressionRequest(t, h, "POST", path, "192.168.1.10:1", nil, other); w.Code != 404 {
				t.Fatalf("wrong session: %d", w.Code)
			}
			if _, ok := s.pendingImports[id]; !ok {
				t.Fatal("wrong session consumed preview")
			}
			newer := s.engine.GetCurrentConfig()
			newer.System.Hostname = "newer-router"
			if _, err := s.engine.ProcessTransaction("intervening-edit", newer); err != nil {
				t.Fatal(err)
			}
			if w := regressionRequest(t, h, "POST", path, "192.168.1.10:1", nil, session); w.Code != 409 {
				t.Fatalf("stale apply: %d %s", w.Code, w.Body.String())
			}
			if s.engine.GetCurrentConfig().System.Hostname != "newer-router" {
				t.Fatal("stale restore overwrote newer config")
			}
		})
	}
}

func TestRecoveryPreviewReplacementExpirationAndBlockers(t *testing.T) {
	s, h := storeBackedTestServer(t)
	session := s.sessionMgr.CreateSession()
	cfg := s.engine.GetCurrentConfig()
	first := recoveryTestPreview(t, s, session, cfg, "backup", nil)
	second := recoveryTestPreview(t, s, session, cfg, "pfsense", nil)
	if _, ok := s.pendingImports[first]; ok {
		t.Fatal("new preview did not replace old source")
	}
	expired := s.pendingImports[second]
	expired.expiresAt = time.Now().Add(-time.Second)
	s.pendingImports[second] = expired
	if w := regressionRequest(t, h, "POST", "/api/v1/import/pfsense/"+second+"/apply", "192.168.1.10:1", nil, session); w.Code != 404 {
		t.Fatal("expired preview accepted")
	}
	cfg.LAN.Interface = "other-interface"
	assessment := s.assessRecovery(cfg, "192.168.1.10:1")
	if assessment.CanApply || len(assessment.Blockers) == 0 {
		t.Fatal("unsupported transition presented as applicable")
	}
}

type recoveryDNSHelper struct {
	mu      sync.Mutex
	policy  dnsfilter.Policy
	fail    bool
	entered chan struct{}
	release chan struct{}
}

func (f *recoveryDNSHelper) Call(ctx context.Context, r dnsfilter.Request, _ func(io.Writer) error) (dnsfilter.Applied, error) {
	if r.Operation == "apply" && f.release != nil {
		f.entered <- struct{}{}
		select {
		case <-f.release:
		case <-ctx.Done():
			return dnsfilter.Applied{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Operation == "apply" {
		if f.fail {
			return dnsfilter.Applied{}, errors.New("test helper rejected DNS update")
		}
		f.policy = r.Policy
		f.policy.Revision++
	}
	return dnsfilter.Applied{Policy: f.policy, Healthy: true}, nil
}

func TestRecoveryPersistsDNSContinuationAndVerifiesAsyncFailureAndRetry(t *testing.T) {
	s, h := storeBackedTestServer(t)
	session := s.sessionMgr.CreateSession()
	cfg := s.engine.GetCurrentConfig()
	cfg.System.Hostname = "restored-router"
	id := recoveryTestPreview(t, s, session, cfg, "backup", &dnsfilter.Policy{}) // Valid v2 null collections.
	w := regressionRequest(t, h, "POST", "/api/v1/import/backup/"+id+"/apply", "192.168.1.10:1", nil, session)
	if w.Code != 200 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	// A fresh Server recovers the DNS continuation without the old browser/session.
	restarted := NewServer(s.engine)
	op, err := restarted.recoveryOperation(context.Background())
	if err != nil || op == nil || op.State != "dns_pending" {
		t.Fatalf("lost continuation: %+v %v", op, err)
	}
	helper := &recoveryDNSHelper{policy: dnsfilter.DefaultPolicy(), fail: true, entered: make(chan struct{}, 1), release: make(chan struct{})}
	service, err := dnsfilter.Open(t.TempDir(), helper, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	s.ConfigureDNSFilter(service)
	path := "/api/v1/recovery/operations/" + op.ID + "/dns"
	w = regressionRequest(t, h, "POST", path, "192.168.1.10:1", nil, session)
	if w.Code != 202 {
		t.Fatalf("DNS: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-helper.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("DNS worker did not start")
	}
	running, err := s.recoveryOperation(context.Background())
	if err != nil || running.State != "dns_running" {
		t.Fatalf("202 falsely reported completion: %+v %v", running, err)
	}
	close(helper.release)
	waitState := func(want string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			got, err := s.recoveryOperation(context.Background())
			if err == nil && got.State == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("never reached %s", want)
	}
	waitState("dns_failed")
	helper.mu.Lock()
	helper.fail = false
	helper.mu.Unlock()
	w = regressionRequest(t, h, "POST", path, "192.168.1.10:1", nil, session)
	if w.Code != 202 {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	waitState("completed")
	// Later unrelated edits must not rewrite this already verified outcome.
	newer := s.engine.GetCurrentConfig()
	newer.System.Hostname = "later-edit"
	if _, err := s.engine.ProcessTransaction("later-edit", newer); err != nil {
		t.Fatal(err)
	}
	finished, err := NewServer(s.engine).recoveryOperation(context.Background())
	if err != nil || finished.State != "completed" {
		t.Fatalf("completion was not retained: %+v %v", finished, err)
	}
}

func TestRecoveryDoesNotStartDNSUntilAccessIsConfirmed(t *testing.T) {
	s, h := storeBackedTestServer(t)
	session := s.sessionMgr.CreateSession()
	cfg := s.engine.GetCurrentConfig()
	cfg.TrustedNetworks = append(cfg.TrustedNetworks, "192.168.2.0/24")
	id := recoveryTestPreview(t, s, session, cfg, "backup", &dnsfilter.Policy{})
	w := regressionRequest(t, h, "POST", "/api/v1/import/backup/"+id+"/apply", "192.168.1.10:1", nil, session)
	if w.Code != 202 {
		t.Fatalf("expected pending: %d %s", w.Code, w.Body.String())
	}
	op, err := s.recoveryOperation(context.Background())
	if err != nil || op.State != "awaiting_confirmation" || op.Deadline == nil {
		t.Fatalf("pending not represented: %+v %v", op, err)
	}
	w = regressionRequest(t, h, "POST", "/api/v1/recovery/operations/"+op.ID+"/dns", "192.168.1.10:1", nil, session)
	if w.Code != 409 {
		t.Fatalf("DNS allowed before confirmation: %d", w.Code)
	}
	if _, err := s.engine.ConfirmTransaction(op.TransactionID); err != nil {
		t.Fatal(err)
	}
	op, err = s.recoveryOperation(context.Background())
	if err != nil || op.State != "dns_pending" {
		t.Fatalf("confirmed continuation: %+v %v", op, err)
	}
	// A restore superseded before its DNS step cannot apply an old policy.
	next := s.engine.GetCurrentConfig()
	next.System.Hostname = "newer-configuration"
	if _, err := s.engine.ProcessTransaction("superseding-edit", next); err != nil {
		t.Fatal(err)
	}
	w = regressionRequest(t, h, "POST", "/api/v1/recovery/operations/"+op.ID+"/dns", "192.168.1.10:1", nil, session)
	if w.Code != 409 {
		t.Fatalf("superseded DNS continuation was accepted: %d", w.Code)
	}
}

func TestSnapshotPreviewRequiresReviewedRevisionAndPreservesName(t *testing.T) {
	s, h := storeBackedTestServer(t)
	session := s.sessionMgr.CreateSession()
	cfg := s.engine.GetCurrentConfig()
	cfg.System.Hostname = "snapshot-host"
	snap, err := s.engine.GetStore().CreateManualSnapshot(cfg, "Before firewall changes")
	if err != nil {
		t.Fatal(err)
	}
	w := regressionRequest(t, h, "GET", "/api/v1/snapshots/"+snap.ID+"/preview", "192.168.1.10:1", nil, session)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "System / management") {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	path := "/api/v1/snapshots/" + snap.ID + "/restore"
	if w = regressionRequest(t, h, "POST", path, "192.168.1.10:1", nil, session); w.Code != 428 {
		t.Fatalf("unreviewed restore accepted: %d", w.Code)
	}
	if w = regressionRequest(t, h, "POST", path, "192.168.1.10:1", map[string]any{"expected_revision": cfg.Revision + 1}, session); w.Code != 409 {
		t.Fatal("stale snapshot preview accepted")
	}
	if w = regressionRequest(t, h, "POST", path, "192.168.1.10:1", map[string]any{"expected_revision": cfg.Revision}, session); w.Code != 200 {
		t.Fatalf("reviewed restore: %d %s", w.Code, w.Body.String())
	}
	w = regressionRequest(t, h, "GET", "/api/v1/snapshots", "192.168.1.10:1", nil, session)
	if !strings.Contains(w.Body.String(), "Before firewall changes") || !strings.Contains(w.Body.String(), `"manual":20`) {
		t.Fatal("name/retention missing")
	}
}

func TestRecoveryRoutesKeepAuthorizationBoundary(t *testing.T) {
	s, h := storeBackedTestServer(t)
	admin := s.sessionMgr.CreateSession()
	readonly := s.sessionMgr.CreateSessionWithMode(true)
	for _, item := range []struct{ method, path string }{{"GET", "/api/v1/recovery/status"}, {"GET", "/api/v1/snapshots/missing/preview"}, {"POST", "/api/v1/recovery/operations/missing/dns"}, {"POST", "/api/v1/recovery/operations/missing/dismiss"}} {
		if w := regressionRequest(t, h, item.method, item.path, "192.168.1.10:1", nil, nil); w.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", item.path, w.Code)
		}
		if w := regressionRequest(t, h, item.method, item.path, "192.168.2.10:1", nil, admin); w.Code != 403 {
			t.Fatalf("untrusted %s: %d", item.path, w.Code)
		}
		w := regressionRequest(t, h, item.method, item.path, "192.168.1.10:1", nil, readonly)
		if item.method == "POST" && w.Code != 403 {
			t.Fatalf("read-only mutation: %d", w.Code)
		}
		if item.path == "/api/v1/recovery/status" && (w.Code != 200 || w.Header().Get("Cache-Control") != "no-store") {
			t.Fatalf("MCP cannot read status: %d", w.Code)
		}
	}
}

func TestRecoveryDiagnosticsCollectsRealBoundedContext(t *testing.T) {
	t.Setenv("MINIMALROUTER_DATA_DIR", t.TempDir())
	s, _ := storeBackedTestServer(t)
	cfg := s.engine.GetCurrentConfig()
	if err := s.engine.GetStore().AppendAuditEvent("recovery.dns_requested", "test", map[string]string{"operation_id": "fixture"}); err != nil {
		t.Fatal(err)
	}
	op := recoveryOperation{ID: "diagnostic", State: "dns_pending", TargetRevision: cfg.Revision, TargetChecksum: configurationFingerprint(cfg), DNSPolicy: &dnsfilter.Policy{Exceptions: []dnsfilter.Exception{{Domain: "private.example.com", Reason: "private reason"}}}}
	if err := s.engine.GetStore().SaveRecoveryOperation(op); err != nil {
		t.Fatal(err)
	}
	data, err := s.buildRecoveryDiagnostics(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"health"`, `"runtime"`, `"storage"`, `"recovery_events"`, `"recovery.dns_requested"`, `"collection_errors"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, unwanted := range []string{"private.example.com", "private reason", `"service_health"`} {
		if strings.Contains(string(data), unwanted) {
			t.Fatalf("diagnostics exposed %s", unwanted)
		}
	}
}
