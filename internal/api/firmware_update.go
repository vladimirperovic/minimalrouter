package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/buildinfo"
	"github.com/vladimirperovic/minimalrouter/internal/firmware"
	"github.com/vladimirperovic/minimalrouter/internal/release"
)

const (
	updateRoot                = "/var/lib/minimalrouter-update"
	updateInbox               = "/var/lib/minimalrouter/update-inbox"
	updatePrivilegeHelper     = "/usr/libexec/minimalrouter/routerd-update"
	updateActivationLog       = "/var/lib/minimalrouter/update.log"
	firmwarePrepareTimeout    = 10 * time.Minute
	firmwareStageTimeout      = 3 * time.Minute
	maxFirmwareUploadBody     = 131 << 20
	maxFirmwareManifestUpload = 1 << 20
	maxFirmwareArchiveUpload  = 128 << 20
	// updateFreeSpaceBytes must cover the compressed download, the expanded
	// payload and the new slot, while current and previous both stay intact.
	updateFreeSpaceBytes = 700 << 20
)

var firmwareUpdateMu sync.Mutex

// blockedReason codes are stable strings the dashboard maps to explanations.
// They exist so the UI never has to parse prose to decide what to offer.
const (
	blockMissingTrustKey     = "missing_trust_key"
	blockMissingHelper       = "missing_update_helper"
	blockMissingBaseline     = "missing_baseline"
	blockPendingActivation   = "pending_activation"
	blockUnsupportedArch     = "unsupported_architecture"
	blockInsufficientSpace   = "insufficient_space"
	blockConfigurationBusy   = "configuration_pending"
	blockRecoveryRequired    = "recovery_required"
	blockUpdateInProgress    = "update_in_progress"
	blockCheckUnavailable    = "check_unavailable"
	blockNoCandidate         = "no_candidate"
	blockAlreadyCurrent      = "already_current"
	blockReadOnlySession     = "read_only_session"
	blockLocalStateUnknown   = "local_state_unavailable"
	blockCandidateSuperseded = "candidate_superseded"
)

type firmwareStatusResponse struct {
	Enabled bool `json:"enabled"`
	// CurrentVersion is the slot the appliance is configured to run;
	// RunningVersion is what this process actually is. They differ between an
	// activation and the restart that completes it, and the dashboard must not
	// claim success from the pointer alone.
	CurrentVersion  string `json:"current_version"`
	RunningVersion  string `json:"running_version"`
	PreviousVersion string `json:"previous_version,omitempty"`
	PendingVersion  string `json:"pending_version,omitempty"`

	Channel         string `json:"channel"`
	LatestVersion   string `json:"latest_version,omitempty"`
	TargetVersion   string `json:"target_version,omitempty"`
	CandidateID     string `json:"candidate_id,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	CanInstall      bool   `json:"can_install"`
	BlockedReason   string `json:"blocked_reason,omitempty"`
	Prerelease      bool   `json:"prerelease,omitempty"`
	PublishedAt     string `json:"published_at,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
	ReleaseNotes    string `json:"release_notes,omitempty"`

	CheckedAt             string `json:"checked_at,omitempty"`
	LastSuccessfulCheckAt string `json:"last_successful_check_at,omitempty"`
	Stale                 bool   `json:"stale"`
	CheckError            string `json:"check_error,omitempty"`
	RateLimited           bool   `json:"rate_limited,omitempty"`
	NextCheckNotBefore    string `json:"next_check_not_before,omitempty"`
	ManualCheckNotBefore  string `json:"manual_check_not_before,omitempty"`

	Operation *UpdateOperation `json:"operation,omitempty"`
}

type boundedCommandOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (w *boundedCommandOutput) Write(p []byte) (int, error) {
	original := len(p)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = w.buffer.Write(p)
	}
	return original, nil
}

func (w *boundedCommandOutput) String() string {
	return strings.TrimSpace(w.buffer.String())
}

func (s *Server) firmwareUpdatesEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.firmwareKey) == ed25519.PublicKeySize
}

// These three read the appliance's own installation. They are variables so a
// test can describe a slot layout, a missing helper or a full disk without
// needing a real appliance; production always uses the fixed paths below.
var applianceUpdateState = func() (firmware.SlotState, error) {
	return (firmware.SlotManager{Root: updateRoot}).State()
}

var updateHelperInstalled = func() bool {
	info, err := os.Stat(updatePrivilegeHelper)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm() == 0o755
}

var availableUpdateBytes = func() (uint64, error) {
	return availableBytes(updateRoot)
}

func currentApplianceVersion(state firmware.SlotState) string {
	if state.Current != "" {
		return state.Current
	}
	return buildinfo.DisplayVersion()
}

func releaseIsNewer(candidate, current string) bool {
	return release.IsNewerVersion(candidate, current)
}

func (s *Server) registerFirmwareRoutes(mux *http.ServeMux) {
	sh := s.securityHeadersMiddleware
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return sh(s.trustedNetworksMiddleware(s.authMiddleware(next)))
	}
	mux.HandleFunc("GET /api/v1/firmware/status", gate(s.handleFirmwareStatus))
	mux.HandleFunc("POST /api/v1/firmware/check", gate(s.handleFirmwareCheck))
	mux.HandleFunc("POST /api/v1/firmware/channel", gate(s.handleFirmwareChannel))
	mux.HandleFunc("POST /api/v1/firmware/update", gate(s.handleFirmwareUpdate))
	mux.HandleFunc("POST /api/v1/firmware/upload", gate(s.handleFirmwareUpload))
}

// updateCapability answers "may an update start right now?" from local state
// only. It is a user-interface aid: every condition is checked again, on the
// server and at the privilege boundary, before anything is installed. A greyed
// out button is not an access control.
type updateCapability struct {
	CanInstall bool
	Reason     string
}

func (s *Server) assessUpdateCapability(state firmware.SlotState, stateErr error, snapshot release.Snapshot, operation *UpdateOperation, now time.Time) updateCapability {
	switch {
	case !s.firmwareUpdatesEnabled():
		return updateCapability{Reason: blockMissingTrustKey}
	case runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64":
		return updateCapability{Reason: blockUnsupportedArch}
	}
	if !updateHelperInstalled() {
		return updateCapability{Reason: blockMissingHelper}
	}
	if stateErr != nil {
		return updateCapability{Reason: blockLocalStateUnknown}
	}
	if state.Current == "" {
		return updateCapability{Reason: blockMissingBaseline}
	}
	if state.Pending != "" {
		return updateCapability{Reason: blockPendingActivation}
	}
	if operation != nil && !operation.State.Terminal() {
		return updateCapability{Reason: blockUpdateInProgress}
	}
	engine := s.engine.GetStatus()
	if engine.RecoveryRequired {
		return updateCapability{Reason: blockRecoveryRequired}
	}
	if engine.Applying || s.engine.GetPendingTransaction() != nil {
		return updateCapability{Reason: blockConfigurationBusy}
	}
	if free, err := availableUpdateBytes(); err == nil && free < updateFreeSpaceBytes {
		return updateCapability{Reason: blockInsufficientSpace}
	}
	if snapshot.LastSuccessAt.IsZero() || snapshot.Stale(now) {
		// Not knowing is not the same as being up to date.
		return updateCapability{Reason: blockCheckUnavailable}
	}
	if snapshot.Candidate == nil {
		return updateCapability{Reason: blockNoCandidate}
	}
	if !releaseIsNewer(snapshot.Candidate.Version, currentApplianceVersion(state)) {
		return updateCapability{Reason: blockAlreadyCurrent}
	}
	return updateCapability{CanInstall: true}
}

func (s *Server) firmwareStatus(now time.Time) firmwareStatusResponse {
	state, stateErr := applianceUpdateState()
	status := firmwareStatusResponse{
		Enabled:        s.firmwareUpdatesEnabled(),
		RunningVersion: buildinfo.DisplayVersion(),
		Channel:        string(s.updateChannel()),
	}
	if stateErr == nil {
		status.CurrentVersion = currentApplianceVersion(state)
		status.PreviousVersion = state.Previous
		status.PendingVersion = state.Pending
	} else {
		status.CurrentVersion = buildinfo.DisplayVersion()
		status.CheckError = "Local update state is unavailable."
	}

	operation, opErr := s.currentUpdateOperation()
	if opErr != nil && status.CheckError == "" {
		status.CheckError = "Update operation state is unavailable."
	}
	status.Operation = operation

	snapshot := s.updateSnapshot()
	status.CheckedAt = formatOptionalTime(snapshot.CheckedAt)
	status.LastSuccessfulCheckAt = formatOptionalTime(snapshot.LastSuccessAt)
	status.Stale = snapshot.Stale(now)
	status.RateLimited = snapshot.RateLimited
	status.NextCheckNotBefore = formatOptionalTime(snapshot.NextEarliest)
	status.ManualCheckNotBefore = formatOptionalTime(snapshot.CooldownExpires)
	if snapshot.Error != "" {
		// The dashboard must show "check unavailable", never "up to date".
		status.CheckError = "The release check did not complete."
	}
	if snapshot.Newest != nil {
		status.LatestVersion = snapshot.Newest.Version
	}
	if snapshot.Candidate != nil {
		status.TargetVersion = snapshot.Candidate.Version
		status.CandidateID = snapshot.Candidate.ID()
		status.Prerelease = snapshot.Candidate.Prerelease
		status.PublishedAt = formatOptionalTime(snapshot.Candidate.PublishedAt)
		status.ReleaseURL = snapshot.Candidate.URL
		status.ReleaseNotes = snapshot.Candidate.Notes
		status.UpdateAvailable = releaseIsNewer(snapshot.Candidate.Version, status.CurrentVersion)
	}

	capability := s.assessUpdateCapability(state, stateErr, snapshot, operation, now)
	status.CanInstall = capability.CanInstall
	status.BlockedReason = capability.Reason
	return status
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func (s *Server) handleFirmwareStatus(w http.ResponseWriter, _ *http.Request) {
	// Answers from cache: rendering the dashboard must never wait on GitHub.
	writeFirmwareJSON(w, http.StatusOK, s.firmwareStatus(time.Now()))
}

func (s *Server) handleFirmwareCheck(w http.ResponseWriter, r *http.Request) {
	if s.sessionIsReadOnly(r) {
		writeFirmwareJSON(w, http.StatusForbidden, map[string]string{"error": "This session is read-only."})
		return
	}
	checker := s.updateChecker()
	if checker == nil {
		writeFirmwareJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Release checking is not configured."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_, err := checker.CheckNow(ctx)
	switch {
	case errors.Is(err, release.ErrCheckTooSoon):
		writeFirmwareJSON(w, http.StatusTooManyRequests, s.firmwareStatus(time.Now()))
		return
	case errors.Is(err, release.ErrCheckInProgress):
		// Another tab is already checking; the shared result is what both see.
		writeFirmwareJSON(w, http.StatusOK, s.firmwareStatus(time.Now()))
		return
	}
	writeFirmwareJSON(w, http.StatusOK, s.firmwareStatus(time.Now()))
}

func (s *Server) handleFirmwareChannel(w http.ResponseWriter, r *http.Request) {
	if s.sessionIsReadOnly(r) {
		writeFirmwareJSON(w, http.StatusForbidden, map[string]string{"error": "This session is read-only."})
		return
	}
	var request struct {
		Channel string `json:"channel"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeFirmwareJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON."})
		return
	}
	channel, ok := release.ParseChannel(request.Channel)
	if !ok {
		writeFirmwareJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Channel must be stable or beta."})
		return
	}
	if err := s.setUpdateChannel(channel); err != nil {
		writeFirmwareJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not store the update channel."})
		return
	}
	s.appendAudit("firmware.channel_changed", auditActor(r.RemoteAddr), map[string]string{"channel": string(channel)})
	writeFirmwareJSON(w, http.StatusOK, s.firmwareStatus(time.Now()))
}

func runUpdateStage(ctx context.Context) (string, error) {
	command := exec.CommandContext(ctx, "/usr/bin/doas", updatePrivilegeHelper, "stage")
	output := &boundedCommandOutput{limit: 32 << 10}
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return output.String(), errors.New("signed update staging timed out")
		}
		return output.String(), fmt.Errorf("signed update staging failed: %w", err)
	}
	return output.String(), nil
}

func activatePendingUpdateDetached() error {
	logFile, err := os.OpenFile(updateActivationLog, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	command := exec.Command("/usr/bin/doas", updatePrivilegeHelper, "activate")
	command.Stdout = logFile
	command.Stderr = logFile
	detachFromSession(command)
	if err := command.Start(); err != nil {
		return errors.Join(err, logFile.Close())
	}
	return logFile.Close()
}

func expectedUpdatePaths() (string, string) {
	return updateInbox + "/release/minimalrouter-linux-" + runtime.GOARCH, updateInbox + "/manifest.json"
}

// stageAndActivate verifies the prepared payload through the privileged helper
// and then starts activation. It is shared by the published-release and the
// offline-upload paths so both get identical verification and identical
// operation reporting.
func (s *Server) stageAndActivate(operationID, current, version, payloadDir, manifestPath string) error {
	expectedPayload, expectedManifest := expectedUpdatePaths()
	if payloadDir != expectedPayload || manifestPath != expectedManifest {
		return s.failOperation(operationID, "inbox_mismatch",
			"The prepared release path did not match the fixed update inbox.")
	}

	if err := s.advanceOperation(operationID, UpdateStaging); err != nil {
		return err
	}
	stageCtx, stageCancel := context.WithTimeout(context.Background(), firmwareStageTimeout)
	stageOutput, err := runUpdateStage(stageCtx)
	stageCancel()
	if err != nil {
		message := "Signed release verification and staging failed."
		if stageOutput != "" {
			message += " " + stageOutput
		}
		return s.failOperation(operationID, "staging_failed", message)
	}

	if err := s.advanceOperation(operationID, UpdateActivating); err != nil {
		return err
	}
	s.appendAudit("firmware.update_staged", "local", map[string]string{"from": current, "to": version})

	// Activation restarts routerd, so this process may not survive to record
	// the outcome. The durable operation record plus the updater's own slot
	// journal are what the next process reconciles against.
	if err := activatePendingUpdateDetached(); err != nil {
		s.appendAudit("firmware.activation_start_failed", "local", map[string]string{"version": version})
		return s.failOperation(operationID, "activation_start_failed",
			"The verified release was staged, but activation could not be started.")
	}
	return nil
}

func beginFirmwareMutation(w http.ResponseWriter) bool {
	if !firmwareUpdateMu.TryLock() {
		writeFirmwareJSON(w, http.StatusConflict, map[string]string{"error": "Another firmware operation is already in progress."})
		return false
	}
	return true
}

type updateRequest struct {
	CandidateID    string `json:"candidate_id"`
	TargetVersion  string `json:"target_version"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (s *Server) handleFirmwareUpdate(w http.ResponseWriter, r *http.Request) {
	if s.sessionIsReadOnly(r) {
		writeFirmwareJSON(w, http.StatusForbidden, map[string]string{"error": "This session is read-only."})
		return
	}
	var request updateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeFirmwareJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON."})
		return
	}
	if strings.TrimSpace(request.CandidateID) == "" || strings.TrimSpace(request.TargetVersion) == "" {
		// The confirmation must name what was confirmed. Without it the server
		// would be free to install whatever is newest at this instant, which is
		// not what the operator approved.
		writeFirmwareJSON(w, http.StatusBadRequest, map[string]string{
			"error":          "The update request must name the confirmed candidate.",
			"blocked_reason": blockCandidateSuperseded,
		})
		return
	}

	if !beginFirmwareMutation(w) {
		return
	}
	defer firmwareUpdateMu.Unlock()

	now := time.Now()
	state, stateErr := applianceUpdateState()
	snapshot := s.updateSnapshot()
	operation, _ := s.currentUpdateOperation()
	capability := s.assessUpdateCapability(state, stateErr, snapshot, operation, now)
	if !capability.CanInstall {
		writeFirmwareJSON(w, http.StatusConflict, map[string]interface{}{
			"error":          "This appliance cannot install an update right now.",
			"blocked_reason": capability.Reason,
			"status":         s.firmwareStatus(now),
		})
		return
	}

	// Re-derive the candidate from the server's own cache. A release published
	// between the dashboard rendering and this request must not be able to take
	// the confirmed one's place.
	candidate := snapshot.Candidate
	if candidate == nil || candidate.ID() != request.CandidateID || candidate.Version != request.TargetVersion {
		writeFirmwareJSON(w, http.StatusConflict, map[string]interface{}{
			"error":          "The confirmed release is no longer the current candidate. Review the new one and confirm again.",
			"blocked_reason": blockCandidateSuperseded,
			"status":         s.firmwareStatus(now),
		})
		return
	}

	current := currentApplianceVersion(state)
	accepted, err := s.beginUpdateOperation(UpdateOperation{
		ID:             newOperationID(now),
		State:          UpdateQueued,
		FromVersion:    current,
		TargetVersion:  candidate.Version,
		CandidateID:    candidate.ID(),
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
		Source:         "published_release",
		StartedAt:      now,
	})
	if errors.Is(err, errUpdateInProgress) {
		writeFirmwareJSON(w, http.StatusConflict, map[string]interface{}{
			"error":          "Another update is already running.",
			"blocked_reason": blockUpdateInProgress,
			"status":         s.firmwareStatus(now),
		})
		return
	}
	if err != nil {
		writeFirmwareJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not record the update operation."})
		return
	}

	if accepted.State.Terminal() || accepted.State != UpdateQueued {
		// A retry of an already-accepted request: report the existing outcome
		// instead of installing a second time.
		writeFirmwareJSON(w, http.StatusAccepted, map[string]interface{}{
			"success":   true,
			"operation": accepted,
			"status":    s.firmwareStatus(now),
		})
		return
	}

	s.appendAudit("firmware.update_accepted", auditActor(r.RemoteAddr), map[string]string{
		"from": current, "to": candidate.Version, "operation": accepted.ID,
	})

	// The work outlives this request deliberately: closing the tab, or losing
	// the response, must not decide whether the appliance updates.
	go s.runPublishedUpdate(*accepted, *candidate)

	writeFirmwareJSON(w, http.StatusAccepted, map[string]interface{}{
		"success":   true,
		"operation": accepted,
		"status":    s.firmwareStatus(now),
	})
}

// runPublishedUpdate downloads, verifies, stages and activates one confirmed
// release. It owns its own context so an aborted HTTP request cannot cancel a
// half-installed update.
func (s *Server) runPublishedUpdate(operation UpdateOperation, candidate release.Release) {
	if err := s.advanceOperation(operation.ID, UpdateDownloading); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), firmwarePrepareTimeout)
	defer cancel()

	payloadDir, manifestPath, err := release.PreparePublished(ctx, candidate, runtime.GOARCH, updateInbox)
	if err != nil {
		_ = s.failOperation(operation.ID, "download_failed",
			"The signed release could not be downloaded or unpacked: "+err.Error())
		return
	}
	if err := s.advanceOperation(operation.ID, UpdateVerifying); err != nil {
		return
	}
	_ = s.stageAndActivate(operation.ID, operation.FromVersion, candidate.Version, payloadDir, manifestPath)
}

func saveFirmwareUpload(reader io.Reader, destination string, maximum int64) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, maximum+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maximum {
		return errors.New("uploaded firmware file exceeds size limit")
	}
	return nil
}

func (s *Server) handleFirmwareUpload(w http.ResponseWriter, r *http.Request) {
	if s.sessionIsReadOnly(r) {
		writeFirmwareJSON(w, http.StatusForbidden, map[string]string{"error": "This session is read-only."})
		return
	}
	if !beginFirmwareMutation(w) {
		return
	}
	defer firmwareUpdateMu.Unlock()

	now := time.Now()
	state, stateErr := applianceUpdateState()
	operation, _ := s.currentUpdateOperation()
	// An upload carries its own payload, so release-check freshness is
	// irrelevant here; every other precondition still applies.
	snapshot := s.updateSnapshot()
	snapshot.LastSuccessAt = now
	snapshot.Candidate = nil
	capability := s.assessUpdateCapability(state, stateErr, snapshot, operation, now)
	if !capability.CanInstall && capability.Reason != blockNoCandidate {
		writeFirmwareJSON(w, http.StatusConflict, map[string]interface{}{
			"error":          "This appliance cannot install an update right now.",
			"blocked_reason": capability.Reason,
		})
		return
	}
	current := currentApplianceVersion(state)

	r.Body = http.MaxBytesReader(w, r.Body, maxFirmwareUploadBody)
	reader, err := r.MultipartReader()
	if err != nil {
		writeFirmwareJSON(w, http.StatusBadRequest, map[string]string{"error": "Upload must be multipart/form-data with manifest and archive files."})
		return
	}
	tempDir, err := os.MkdirTemp("/var/lib/minimalrouter", ".firmware-upload-")
	if err != nil {
		writeFirmwareJSON(w, http.StatusInsufficientStorage, map[string]string{"error": "Could not allocate private upload staging space."})
		return
	}
	defer os.RemoveAll(tempDir)
	manifestSource := tempDir + "/manifest.json"
	archiveSource := tempDir + "/release.tar.gz"
	manifestSeen := false
	archiveSeen := false

	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			var maxErr *http.MaxBytesError
			if errors.As(nextErr, &maxErr) {
				writeFirmwareJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Firmware upload exceeds the 131 MiB request limit."})
			} else {
				writeFirmwareJSON(w, http.StatusBadRequest, map[string]string{"error": "Could not read firmware upload."})
			}
			return
		}
		name := part.FormName()
		switch name {
		case "manifest":
			if manifestSeen || part.FileName() == "" {
				_ = part.Close()
				writeFirmwareJSON(w, http.StatusBadRequest, map[string]string{"error": "Exactly one signed manifest file is required."})
				return
			}
			manifestSeen = true
			err = saveFirmwareUpload(part, manifestSource, maxFirmwareManifestUpload)
		case "archive":
			if archiveSeen || part.FileName() == "" {
				_ = part.Close()
				writeFirmwareJSON(w, http.StatusBadRequest, map[string]string{"error": "Exactly one release archive file is required."})
				return
			}
			archiveSeen = true
			err = saveFirmwareUpload(part, archiveSource, maxFirmwareArchiveUpload)
		default:
			_ = part.Close()
			writeFirmwareJSON(w, http.StatusBadRequest, map[string]string{"error": "Firmware upload contains an unexpected form field."})
			return
		}
		_ = part.Close()
		if err != nil {
			if strings.Contains(err.Error(), "size limit") {
				writeFirmwareJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
			} else {
				writeFirmwareJSON(w, http.StatusBadRequest, map[string]string{"error": "Could not store firmware upload."})
			}
			return
		}
	}
	if !manifestSeen || !archiveSeen {
		writeFirmwareJSON(w, http.StatusBadRequest, map[string]string{"error": "Both signed manifest and release archive files are required."})
		return
	}

	payloadDir, manifestPath, version, err := release.PrepareLocalRelease(manifestSource, archiveSource, runtime.GOARCH, updateInbox)
	if err != nil {
		writeFirmwareJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Could not prepare uploaded release: " + err.Error()})
		return
	}
	if !releaseIsNewer(version, current) {
		writeFirmwareJSON(w, http.StatusConflict, map[string]string{"error": "Uploaded build must have a version newer than the currently active release."})
		return
	}

	accepted, err := s.beginUpdateOperation(UpdateOperation{
		ID:            newOperationID(now),
		State:         UpdateQueued,
		FromVersion:   current,
		TargetVersion: version,
		Source:        "upload",
		StartedAt:     now,
	})
	if err != nil {
		writeFirmwareJSON(w, http.StatusConflict, map[string]string{"error": "Another update is already running."})
		return
	}
	s.appendAudit("firmware.update_accepted", auditActor(r.RemoteAddr), map[string]string{
		"from": current, "to": version, "operation": accepted.ID, "source": "upload",
	})

	if err := s.stageAndActivate(accepted.ID, current, version, payloadDir, manifestPath); err != nil {
		writeFirmwareJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error":     "The uploaded release was not installed.",
			"operation": s.mustOperation(accepted.ID),
		})
		return
	}
	writeFirmwareJSON(w, http.StatusAccepted, map[string]interface{}{
		"success":   true,
		"operation": s.mustOperation(accepted.ID),
		"state":     "activating",
		"version":   version,
		"message":   "Signed release verified and staged. Minimal Router is restarting into the new slot.",
	})
}

func newOperationID(now time.Time) string {
	return fmt.Sprintf("upd-%d", now.UnixNano())
}

func writeFirmwareJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// sessionIsReadOnly keeps observer sessions to observation. The privilege
// boundary is still the helper; this only avoids offering an action the
// session may not take.
func (s *Server) sessionIsReadOnly(r *http.Request) bool {
	session, err := s.sessionMgr.ValidateSession(r)
	if err != nil || session == nil {
		return true
	}
	return session.ReadOnly
}
