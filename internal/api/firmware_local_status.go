package api

import (
	"net/http"
)

func (s *Server) handleFirmwareLocalStatus(w http.ResponseWriter, _ *http.Request) {
	state, err := applianceUpdateState()
	if err != nil {
		writeFirmwareJSON(w, http.StatusServiceUnavailable, firmwareStatusResponse{
			Enabled:    s.firmwareUpdatesEnabled(),
			CheckError: "Local update state is unavailable.",
		})
		return
	}
	writeFirmwareJSON(w, http.StatusOK, firmwareStatusResponse{
		Enabled:         s.firmwareUpdatesEnabled(),
		CurrentVersion:  currentApplianceVersion(state),
		PreviousVersion: state.Previous,
		PendingVersion:  state.Pending,
	})
}
