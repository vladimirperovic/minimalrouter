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
	"syscall"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/buildinfo"
	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

const (
	updateRoot                = "/var/lib/minimalrouter-update"
	updateInbox               = "/var/lib/minimalrouter/update-inbox"
	updatePrivilegeHelper     = "/usr/libexec/minimalrouter/routerd-update"
	updateActivationLog       = "/var/lib/minimalrouter/update.log"
	firmwareStatusTimeout     = 20 * time.Second
	firmwarePrepareTimeout    = 5 * time.Minute
	firmwareStageTimeout      = 3 * time.Minute
	maxFirmwareUploadBody     = 131 << 20
	maxFirmwareManifestUpload = 1 << 20
	maxFirmwareArchiveUpload  = 128 << 20
)

var firmwareUpdateMu sync.Mutex

type firmwareStatusResponse struct {
	Enabled         bool   `json:"enabled"`
	CurrentVersion  string `json:"current_version"`
	PreviousVersion string `json:"previous_version,omitempty"`
	PendingVersion  string `json:"pending_version,omitempty"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	Prerelease      bool   `json:"prerelease,omitempty"`
	PublishedAt     string `json:"published_at,omitempty"`
	CheckError      string `json:"check_error,omitempty"`
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

func applianceUpdateState() (firmware.SlotState, error) {
	return (firmware.SlotManager{Root: updateRoot}).State()
}

func currentApplianceVersion(state firmware.SlotState) string {
	if state.Current != "" {
		return state.Current
	}
	return buildinfo.DisplayVersion()
}

func releaseIsNewer(candidate, current string) bool {
	if !firmware.IsReleaseVersion(candidate) || !firmware.IsReleaseVersion(current) {
		return false
	}
	cmp, err := firmware.CompareReleaseVersions(candidate, current)
	return err == nil && cmp > 0
}

func (s *Server) registerFirmwareRoutes(mux *http.ServeMux) {
	sh := s.securityHeadersMiddleware
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return sh(s.trustedNetworksMiddleware(s.authMiddleware(next)))
	}
	mux.HandleFunc("GET /api/v1/firmware/status", gate(s.handleFirmwareStatus))
	mux.HandleFunc("POST /api/v1/firmware/update", gate(s.handleFirmwareUpdate))
	mux.HandleFunc("POST /api/v1/firmware/upload", gate(s.handleFirmwareUpload))
}

func (s *Server) handleFirmwareStatus(w http.ResponseWriter, r *http.Request) {
	state, err := applianceUpdateState()
	if err != nil {
		writeFirmwareJSON(w, http.StatusServiceUnavailable, firmwareStatusResponse{
			Enabled:    s.firmwareUpdatesEnabled(),
			CheckError: "Local update state is unavailable.",
		})
		return
	}
	status := firmwareStatusResponse{
		Enabled:         s.firmwareUpdatesEnabled(),
		CurrentVersion:  currentApplianceVersion(state),
		PreviousVersion: state.Previous,
		PendingVersion:  state.Pending,
	}
	if !status.Enabled {
		status.CheckError = "Signed firmware trust key is not installed."
		writeFirmwareJSON(w, http.StatusOK, status)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), firmwareStatusTimeout)
	defer cancel()
	release, err := firmware.LatestPublishedRelease(ctx)
	if err != nil {
		status.CheckError = "Could not check published releases."
		writeFirmwareJSON(w, http.StatusOK, status)
		return
	}
	status.LatestVersion = release.Version
	status.Prerelease = release.Prerelease
	if !release.PublishedAt.IsZero() {
		status.PublishedAt = release.PublishedAt.UTC().Format(time.RFC3339)
	}
	status.UpdateAvailable = releaseIsNewer(release.Version, status.CurrentVersion)
	writeFirmwareJSON(w, http.StatusOK, status)
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
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return errors.Join(err, logFile.Close())
	}
	return logFile.Close()
}

func (s *Server) updatePrerequisites(w http.ResponseWriter) (firmware.SlotState, string, bool) {
	if !s.firmwareUpdatesEnabled() {
		writeFirmwareJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Signed firmware updates are disabled because no trusted key is installed."})
		return firmware.SlotState{}, "", false
	}
	if info, err := os.Stat(updatePrivilegeHelper); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o755 {
		writeFirmwareJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Signed web-update privilege is not installed. Run the full signed distribution installer once to enable dashboard updates."})
		return firmware.SlotState{}, "", false
	}
	state, err := applianceUpdateState()
	if err != nil {
		writeFirmwareJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Local update state is unavailable."})
		return firmware.SlotState{}, "", false
	}
	current := currentApplianceVersion(state)
	if state.Current == "" {
		writeFirmwareJSON(w, http.StatusConflict, map[string]string{"error": "No A/B rollback baseline is registered. Run the full signed distribution installer before web updates."})
		return firmware.SlotState{}, "", false
	}
	if state.Pending != "" {
		writeFirmwareJSON(w, http.StatusConflict, map[string]string{"error": "A verified release is already pending activation."})
		return firmware.SlotState{}, "", false
	}
	return state, current, true
}

func expectedUpdatePaths() (string, string) {
	return updateInbox + "/release/minimalrouter-linux-" + runtime.GOARCH, updateInbox + "/manifest.json"
}

func (s *Server) stageAndActivate(w http.ResponseWriter, r *http.Request, current, version, payloadDir, manifestPath string) {
	expectedPayload, expectedManifest := expectedUpdatePaths()
	if payloadDir != expectedPayload || manifestPath != expectedManifest {
		writeFirmwareJSON(w, http.StatusInternalServerError, map[string]string{"error": "Prepared release path did not match the fixed update inbox."})
		return
	}

	stageCtx, stageCancel := context.WithTimeout(context.Background(), firmwareStageTimeout)
	stageOutput, err := runUpdateStage(stageCtx)
	stageCancel()
	if err != nil {
		message := "Signed release verification/staging failed."
		if stageOutput != "" {
			message += " " + stageOutput
		}
		writeFirmwareJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": message})
		return
	}

	go func(targetVersion string) {
		time.Sleep(1500 * time.Millisecond)
		if err := activatePendingUpdateDetached(); err != nil {
			s.appendAudit("firmware.activation_start_failed", "local", map[string]string{"version": targetVersion})
		}
	}(version)

	s.appendAudit("firmware.update_staged", auditActor(r.RemoteAddr), map[string]string{
		"from": current,
		"to":   version,
	})
	writeFirmwareJSON(w, http.StatusAccepted, map[string]interface{}{
		"success": true,
		"state":   "activating",
		"version": version,
		"message": "Signed release verified and staged. Minimal Router is restarting into the new slot.",
	})
}

func beginFirmwareMutation(w http.ResponseWriter) bool {
	if !firmwareUpdateMu.TryLock() {
		writeFirmwareJSON(w, http.StatusConflict, map[string]string{"error": "Another firmware operation is already in progress."})
		return false
	}
	return true
}

func (s *Server) handleFirmwareUpdate(w http.ResponseWriter, r *http.Request) {
	if !beginFirmwareMutation(w) {
		return
	}
	defer firmwareUpdateMu.Unlock()
	_, current, ok := s.updatePrerequisites(w)
	if !ok {
		return
	}

	prepareCtx, prepareCancel := context.WithTimeout(r.Context(), firmwarePrepareTimeout)
	defer prepareCancel()
	release, err := firmware.LatestPublishedRelease(prepareCtx)
	if err != nil {
		writeFirmwareJSON(w, http.StatusBadGateway, map[string]string{"error": "Could not check published releases."})
		return
	}
	if !releaseIsNewer(release.Version, current) {
		writeFirmwareJSON(w, http.StatusConflict, map[string]string{"error": "This router is already on the newest published release."})
		return
	}
	payloadDir, manifestPath, err := firmware.PreparePublishedRelease(prepareCtx, release, runtime.GOARCH, updateInbox)
	if err != nil {
		writeFirmwareJSON(w, http.StatusBadGateway, map[string]string{"error": "Could not prepare the signed release: " + err.Error()})
		return
	}
	s.stageAndActivate(w, r, current, release.Version, payloadDir, manifestPath)
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
	if !beginFirmwareMutation(w) {
		return
	}
	defer firmwareUpdateMu.Unlock()
	_, current, ok := s.updatePrerequisites(w)
	if !ok {
		return
	}

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

	payloadDir, manifestPath, version, err := firmware.PrepareLocalRelease(manifestSource, archiveSource, runtime.GOARCH, updateInbox)
	if err != nil {
		writeFirmwareJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Could not prepare uploaded release: " + err.Error()})
		return
	}
	if !releaseIsNewer(version, current) {
		writeFirmwareJSON(w, http.StatusConflict, map[string]string{"error": "Uploaded build must have a version newer than the currently active release."})
		return
	}
	s.stageAndActivate(w, r, current, version, payloadDir, manifestPath)
}

func writeFirmwareJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
