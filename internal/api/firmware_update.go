package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/buildinfo"
	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

const (
	updateRoot             = "/var/lib/minimalrouter-update"
	updateInbox            = "/var/lib/minimalrouter/update-inbox"
	updatePrivilegeHelper  = "/usr/libexec/minimalrouter/routerd-update"
	updateActivationLog    = "/var/lib/minimalrouter/update.log"
	firmwareStatusTimeout  = 20 * time.Second
	firmwarePrepareTimeout = 5 * time.Minute
	firmwareStageTimeout   = 3 * time.Minute
)

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
		logFile.Close()
		return err
	}
	return logFile.Close()
}

func (s *Server) handleFirmwareUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.firmwareUpdatesEnabled() {
		writeFirmwareJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Signed firmware updates are disabled because no trusted key is installed."})
		return
	}
	if info, err := os.Stat(updatePrivilegeHelper); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o755 {
		writeFirmwareJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Signed web-update privilege is not installed. Install this release once through the supported signed CLI path first."})
		return
	}
	state, err := applianceUpdateState()
	if err != nil {
		writeFirmwareJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Local update state is unavailable."})
		return
	}
	current := currentApplianceVersion(state)
	if state.Current == "" {
		writeFirmwareJSON(w, http.StatusConflict, map[string]string{"error": "No A/B rollback baseline is registered. Run the full signed distribution installer before web updates."})
		return
	}
	if state.Pending != "" {
		writeFirmwareJSON(w, http.StatusConflict, map[string]string{"error": "A verified release is already pending activation."})
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
	// The root helper uses fixed paths by design. Assert that the downloader did
	// not return a different location before crossing the privilege boundary.
	expectedPayload := updateInbox + "/release/minimalrouter-linux-" + runtime.GOARCH
	if payloadDir != expectedPayload || manifestPath != updateInbox+"/manifest.json" {
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

	// Return the 202 response before activation restarts routerd. The detached
	// root updater reads the root-owned pending version, verifies runtime-layout
	// compatibility, switches both daemons together, and automatically rolls
	// back if the new pair does not become healthy.
	go func() {
		time.Sleep(1500 * time.Millisecond)
		if err := activatePendingUpdateDetached(); err != nil {
			s.appendAudit("firmware.activation_start_failed", "local", map[string]string{"version": release.Version})
		}
	}()

	s.appendAudit("firmware.update_staged", auditActor(r.RemoteAddr), map[string]string{
		"from": current,
		"to":   release.Version,
	})
	writeFirmwareJSON(w, http.StatusAccepted, map[string]interface{}{
		"success": true,
		"state":   "activating",
		"version": release.Version,
		"message": "Signed release verified and staged. Minimal Router is restarting into the new slot.",
	})
}

func writeFirmwareJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
