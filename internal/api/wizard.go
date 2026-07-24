package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// WizardSetupRequest contains the first-run installation parameters per DESIGN.md §14.
type WizardSetupRequest struct {
	WANInterface  string `json:"wan_interface"`
	PPPoEUsername string `json:"pppoe_username"`
	PPPoEPassword string `json:"pppoe_password"`
	AdminPassword string `json:"admin_password"`
	LANInterface  string `json:"lan_interface"`
	LANIPAddress  string `json:"lan_ip_address"` // e.g. "192.168.1.1"
}

// RegisterWizardRoutes attaches setup wizard endpoints.
func (s *Server) RegisterWizardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/setup/status", s.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup/apply", s.handleSetupApply)
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.GetCurrentConfig()
	isConfigured := cfg.WAN.Username != ""

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"is_configured": isConfigured,
		"wan_interface": cfg.WAN.Interface,
		"lan_interface": cfg.LAN.Interface,
		"lan_ip":        cfg.LAN.IPAddress,
	})
}

func (s *Server) handleSetupApply(w http.ResponseWriter, r *http.Request) {
	var req WizardSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Validate Admin Password length (SECURITY.md §5)
	if len([]rune(req.AdminPassword)) < 15 {
		http.Error(w, "Administrator password must be at least 15 characters long", http.StatusUnprocessableEntity)
		return
	}

	_, err := auth.HashPassword(req.AdminPassword)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to hash password: %v", err), http.StatusInternalServerError)
		return
	}

	cfg := config.DefaultConfig()

	if req.WANInterface != "" {
		cfg.WAN.Interface = req.WANInterface
	}
	cfg.WAN.Username = req.PPPoEUsername
	cfg.WAN.Password = req.PPPoEPassword

	if req.LANInterface != "" {
		cfg.LAN.Interface = req.LANInterface
	}
	if req.LANIPAddress != "" {
		cfg.LAN.IPAddress = req.LANIPAddress
		cfg.LAN.CIDR = fmt.Sprintf("%s/24", req.LANIPAddress)
	}

	txID := fmt.Sprintf("wizard-setup-%d", cfg.Revision)
	tx, err := s.engine.ProcessTransaction(txID, cfg)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Wizard apply failed: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"redirect_url": fmt.Sprintf("https://%s", cfg.LAN.IPAddress),
		"tx":           tx,
	})
}
