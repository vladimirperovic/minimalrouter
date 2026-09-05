package api

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/firmware"
	"github.com/vladimirperovic/minimalrouter/internal/release"
)

// updateTestServer builds a server whose appliance state, helper presence and
// free space are described by the test rather than read from a real
// installation.
func updateTestServer(t *testing.T) *Server {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "firmware-api-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tempDir) })

	store, err := config.NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	engine := apply.NewEngineWithClient(config.DefaultConfig(), store, apiTestApplyClient{})
	server := NewServer(engine)
	server.firmwareKey = make(ed25519.PublicKey, ed25519.PublicKeySize)
	server.updates.operations = newUpdateOperationStore(filepath.Join(tempDir, "update-operation.json"))
	server.updates.preferences = newUpdatePreferences(filepath.Join(tempDir, "update-preferences.json"))

	previousState, previousHelper, previousSpace := applianceUpdateState, updateHelperInstalled, availableUpdateBytes
	applianceUpdateState = func() (firmware.SlotState, error) {
		return firmware.SlotState{Current: "0.1.7", Previous: "0.1.6"}, nil
	}
	updateHelperInstalled = func() bool { return true }
	availableUpdateBytes = func() (uint64, error) { return 4 << 30, nil }
	t.Cleanup(func() {
		applianceUpdateState, updateHelperInstalled, availableUpdateBytes = previousState, previousHelper, previousSpace
	})
	return server
}

// stubChecker gives the server a cached snapshot without any HTTP traffic.
func stubChecker(t *testing.T, server *Server, body string) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	checker := release.NewChecker(release.Catalog{APIURL: upstream.URL}, "amd64",
		func() release.Channel { return release.ChannelBeta })
	if _, err := checker.CheckNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	server.updates.checker = checker
}

func candidateReleaseJSON(version string) string {
	return `[{"tag_name":"v` + version + `","draft":false,"prerelease":false,` +
		`"published_at":"2026-09-05T10:00:00Z","body":"notes",` +
		`"assets":[{"name":"minimalrouter-linux-amd64.tar.gz"},{"name":"minimalrouter-linux-amd64.manifest.json"}]}]`
}

func decodeStatus(t *testing.T, recorder *httptest.ResponseRecorder) firmwareStatusResponse {
	t.Helper()
	var status firmwareStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v (body %s)", err, recorder.Body.String())
	}
	return status
}

func TestStatusReportsAnInstallableCandidateWithoutCallingUpstream(t *testing.T) {
	server := updateTestServer(t)
	stubChecker(t, server, candidateReleaseJSON("0.1.8"))

	status := server.firmwareStatus(time.Now())
	if !status.UpdateAvailable || status.TargetVersion != "0.1.8" {
		t.Fatalf("status = %+v, want an available 0.1.8", status)
	}
	if status.CandidateID == "" {
		t.Fatal("the dashboard must receive a candidate id it can confirm")
	}
	if !status.CanInstall || status.BlockedReason != "" {
		t.Fatalf("install should be possible, got blocked_reason=%q", status.BlockedReason)
	}
	if status.CurrentVersion != "0.1.7" {
		t.Fatalf("current version = %q, want the active slot", status.CurrentVersion)
	}
}

func TestStatusNeverPresentsAFailedCheckAsUpToDate(t *testing.T) {
	server := updateTestServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()
	checker := release.NewChecker(release.Catalog{APIURL: upstream.URL}, "amd64",
		func() release.Channel { return release.ChannelStable })
	_, _ = checker.CheckNow(t.Context())
	server.updates.checker = checker

	status := server.firmwareStatus(time.Now())
	if status.UpdateAvailable {
		t.Fatal("a failed check must not claim an update is available")
	}
	if status.CheckError == "" {
		t.Fatal("a failed check must be visible to the operator")
	}
	if status.CanInstall || status.BlockedReason != blockCheckUnavailable {
		t.Fatalf("blocked reason = %q, want %q", status.BlockedReason, blockCheckUnavailable)
	}
}

func TestCapabilityReportsEachLocalObstacle(t *testing.T) {
	now := time.Now()
	healthy := firmware.SlotState{Current: "0.1.7"}
	fresh := release.Snapshot{
		LastSuccessAt: now,
		StaleAfter:    release.DefaultStaleAfter,
		Candidate:     &release.Release{Version: "0.1.8", Tag: "v0.1.8"},
	}

	for name, tc := range map[string]struct {
		prepare   func(t *testing.T, server *Server)
		state     firmware.SlotState
		stateErr  error
		snapshot  release.Snapshot
		operation *UpdateOperation
		want      string
	}{
		"no trust key": {
			prepare: func(_ *testing.T, server *Server) { server.firmwareKey = nil },
			state:   healthy, snapshot: fresh, want: blockMissingTrustKey,
		},
		"helper missing": {
			prepare: func(t *testing.T, _ *Server) {
				previous := updateHelperInstalled
				updateHelperInstalled = func() bool { return false }
				t.Cleanup(func() { updateHelperInstalled = previous })
			},
			state: healthy, snapshot: fresh, want: blockMissingHelper,
		},
		"no rollback baseline": {
			state: firmware.SlotState{}, snapshot: fresh, want: blockMissingBaseline,
		},
		"activation already pending": {
			state: firmware.SlotState{Current: "0.1.7", Pending: "0.1.8"}, snapshot: fresh, want: blockPendingActivation,
		},
		"local state unreadable": {
			state: healthy, stateErr: os.ErrPermission, snapshot: fresh, want: blockLocalStateUnknown,
		},
		"update already running": {
			state: healthy, snapshot: fresh,
			operation: &UpdateOperation{ID: "upd-1", State: UpdateDownloading},
			want:      blockUpdateInProgress,
		},
		"disk too full": {
			prepare: func(t *testing.T, _ *Server) {
				previous := availableUpdateBytes
				availableUpdateBytes = func() (uint64, error) { return 10 << 20, nil }
				t.Cleanup(func() { availableUpdateBytes = previous })
			},
			state: healthy, snapshot: fresh, want: blockInsufficientSpace,
		},
		"check never succeeded": {
			state: healthy, snapshot: release.Snapshot{StaleAfter: release.DefaultStaleAfter}, want: blockCheckUnavailable,
		},
		"no candidate published": {
			state:    healthy,
			snapshot: release.Snapshot{LastSuccessAt: now, StaleAfter: release.DefaultStaleAfter},
			want:     blockNoCandidate,
		},
		"already on the newest": {
			state: firmware.SlotState{Current: "0.1.8"}, snapshot: fresh, want: blockAlreadyCurrent,
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := updateTestServer(t)
			if tc.prepare != nil {
				tc.prepare(t, server)
			}
			capability := server.assessUpdateCapability(tc.state, tc.stateErr, tc.snapshot, tc.operation, now)
			if capability.Reason != tc.want {
				t.Fatalf("blocked reason = %q, want %q", capability.Reason, tc.want)
			}
			if capability.CanInstall {
				t.Fatal("a blocked appliance must not report that it can install")
			}
		})
	}
}

// adminRequest builds a request carrying a full administrator session, the way
// authMiddleware would have validated it before these handlers run.
func adminRequest(t *testing.T, server *Server, method, path, body string) *http.Request {
	t.Helper()
	session := server.sessionMgr.CreateSession()
	if session == nil {
		t.Fatal("could not create a test session")
	}
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	return request
}

// readOnlyRequest builds an observer session, which may look but not install.
func readOnlyRequest(t *testing.T, server *Server, method, path, body string) *http.Request {
	t.Helper()
	session := server.sessionMgr.CreateSessionWithMode(true)
	if session == nil {
		t.Fatal("could not create a read-only test session")
	}
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	return request
}

func TestUpdateRefusesAConfirmationWithoutACandidate(t *testing.T) {
	server := updateTestServer(t)
	stubChecker(t, server, candidateReleaseJSON("0.1.8"))

	recorder := httptest.NewRecorder()
	request := adminRequest(t, server, http.MethodPost, "/api/v1/firmware/update", `{}`)
	server.handleFirmwareUpdate(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: an update must name what was confirmed", recorder.Code)
	}
}

// The operator confirmed a specific release. If a newer one is published in
// between, the server must refuse rather than install something else.
func TestUpdateRefusesAStaleConfirmation(t *testing.T) {
	server := updateTestServer(t)
	stubChecker(t, server, candidateReleaseJSON("0.1.9"))

	recorder := httptest.NewRecorder()
	body := `{"candidate_id":"an-older-candidate","target_version":"0.1.8"}`
	request := adminRequest(t, server, http.MethodPost, "/api/v1/firmware/update", body)
	server.handleFirmwareUpdate(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
	var response struct {
		BlockedReason string `json:"blocked_reason"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.BlockedReason != blockCandidateSuperseded {
		t.Fatalf("blocked reason = %q, want %q", response.BlockedReason, blockCandidateSuperseded)
	}
	if operation, _ := server.currentUpdateOperation(); operation != nil {
		t.Fatalf("a refused confirmation must not start an operation, got %+v", operation)
	}
}

func TestUpdateRefusesWhileAConfigurationChangeIsPending(t *testing.T) {
	server := updateTestServer(t)
	stubChecker(t, server, candidateReleaseJSON("0.1.8"))
	status := server.firmwareStatus(time.Now())

	// A configuration transaction awaiting confirmation must not be raced by a
	// slot activation that restarts both services.
	previous := applianceUpdateState
	applianceUpdateState = func() (firmware.SlotState, error) {
		return firmware.SlotState{Current: "0.1.7", Pending: "0.1.8"}, nil
	}
	defer func() { applianceUpdateState = previous }()

	recorder := httptest.NewRecorder()
	body := `{"candidate_id":"` + status.CandidateID + `","target_version":"0.1.8"}`
	request := adminRequest(t, server, http.MethodPost, "/api/v1/firmware/update", body)
	server.handleFirmwareUpdate(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
}

// An observer session may watch an update, never start one.
func TestReadOnlySessionCannotStartOrRedirectAnUpdate(t *testing.T) {
	server := updateTestServer(t)
	stubChecker(t, server, candidateReleaseJSON("0.1.8"))
	status := server.firmwareStatus(time.Now())

	for name, call := range map[string]struct {
		path    string
		body    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		"install": {
			path:    "/api/v1/firmware/update",
			body:    `{"candidate_id":"` + status.CandidateID + `","target_version":"0.1.8"}`,
			handler: server.handleFirmwareUpdate,
		},
		"change channel": {
			path:    "/api/v1/firmware/channel",
			body:    `{"channel":"stable"}`,
			handler: server.handleFirmwareChannel,
		},
		"force a check": {
			path:    "/api/v1/firmware/check",
			body:    `{}`,
			handler: server.handleFirmwareCheck,
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			call.handler(recorder, readOnlyRequest(t, server, http.MethodPost, call.path, call.body))
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", recorder.Code)
			}
		})
	}
	if operation, _ := server.currentUpdateOperation(); operation != nil {
		t.Fatalf("a read-only session must not have started anything, got %+v", operation)
	}
}

func TestChannelSelectionIsStoredAndReflectedInStatus(t *testing.T) {
	server := updateTestServer(t)
	stubChecker(t, server, candidateReleaseJSON("0.1.8"))

	recorder := httptest.NewRecorder()
	request := adminRequest(t, server, http.MethodPost, "/api/v1/firmware/channel", `{"channel":"stable"}`)
	server.handleFirmwareChannel(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	if decodeStatus(t, recorder).Channel != "stable" {
		t.Fatal("the stored channel must be reflected in the status")
	}

	recorder = httptest.NewRecorder()
	request = adminRequest(t, server, http.MethodPost, "/api/v1/firmware/channel", `{"channel":"nightly"}`)
	server.handleFirmwareChannel(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown channel status = %d, want 422", recorder.Code)
	}
}

func TestStartupReconciliationReadsTheOutcomeFromTheSlotState(t *testing.T) {
	now := time.Now()
	for name, tc := range map[string]struct {
		current string
		want    UpdateState
	}{
		"activation completed":    {current: "0.1.8", want: UpdateSucceeded},
		"activation rolled back":  {current: "0.1.7", want: UpdateRolledBack},
		"neither version is live": {current: "0.1.5", want: UpdateRecoveryRequired},
	} {
		t.Run(name, func(t *testing.T) {
			server := updateTestServer(t)
			store := server.updates.operations
			operation := sampleOperation("upd-1", now)
			if _, err := store.Begin(operation); err != nil {
				t.Fatal(err)
			}
			if err := store.Advance(operation.ID, UpdateActivating, now); err != nil {
				t.Fatal(err)
			}

			previous := applianceUpdateState
			applianceUpdateState = func() (firmware.SlotState, error) {
				return firmware.SlotState{Current: tc.current, Previous: "0.1.7"}, nil
			}
			defer func() { applianceUpdateState = previous }()

			// A fresh process reads the same durable record.
			restarted := newUpdateOperationStore(store.path)
			server.updates.operations = restarted
			server.reconcileInterruptedUpdate(restarted, now)

			result, err := restarted.Current()
			if err != nil {
				t.Fatal(err)
			}
			if result.State != tc.want {
				t.Fatalf("state = %s, want %s", result.State, tc.want)
			}
		})
	}
}
