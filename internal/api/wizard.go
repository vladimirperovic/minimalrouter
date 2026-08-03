package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
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

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	isConfigured := s.adminHash != ""
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if isConfigured {
		// Once setup is complete this endpoint is only an unauthenticated routing
		// hint for AuthGate. Do not expose internal interfaces, addresses or
		// recovery details to an unauthenticated LAN client.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"is_configured": true,
		})
		return
	}

	cfg := s.engine.GetCurrentConfig()
	status := s.engine.GetStatus()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"is_configured":     false,
		"wan_interface":     cfg.WAN.Interface,
		"lan_interface":     cfg.LAN.Interface,
		"lan_ip":            cfg.LAN.IPAddress,
		"recovery_required": status.RecoveryRequired,
		"recovery_reason":   status.RecoveryReason,
	})
}

func (s *Server) handleSetupApply(w http.ResponseWriter, r *http.Request) {
	// SECURITY: Rate limit setup requests to prevent brute force
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if !s.rateLimiter.Allow(ip) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": "Too many setup attempts. Please wait."})
		return
	}

	// SECURITY: Guard against re-running wizard after initial setup per SECURITY.md §8
	s.mu.RLock()
	alreadyConfigured := s.adminHash != ""
	s.mu.RUnlock()

	if alreadyConfigured {
		log.Printf("[AUTH] Blocked wizard re-run attempt from %s\n", r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "System is already configured. Use the dashboard to change settings.",
		})
		return
	}
	if status := s.engine.GetStatus(); status.RecoveryRequired {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error":  "Initial setup is blocked until local recovery succeeds.",
			"reason": status.RecoveryReason,
		})
		return
	}

	var req WizardSetupRequest
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Validate Admin Password length (SECURITY.md §5)
	if len([]rune(req.AdminPassword)) < 12 {
		http.Error(w, "Administrator password must be at least 12 characters long", http.StatusUnprocessableEntity)
		return
	}
	if (req.PPPoEUsername == "") != (req.PPPoEPassword == "") {
		http.Error(w, "Provide both PPPoE username and password, or leave both empty", http.StatusUnprocessableEntity)
		return
	}

	// Hash admin password with Argon2id and STORE it
	hashedPassword, err := auth.HashPassword(req.AdminPassword)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to hash password: %v", err), http.StatusInternalServerError)
		return
	}

	cfg := config.DefaultConfig()

	// Optional external and wireless integrations are always opt-in. Keep them
	// explicitly disabled during first-run setup even if another default changes
	// in a future release.
	cfg.Cloudflare.DDNSEnabled = false
	cfg.Cloudflare.TunnelEnabled = false
	cfg.WiFi.Enabled = false

	if req.WANInterface != "" {
		cfg.WAN.Interface = req.WANInterface
	}
	cfg.WAN.Username = req.PPPoEUsername
	cfg.WAN.Password = req.PPPoEPassword
	cfg.WAN.Enabled = req.PPPoEUsername != "" || req.PPPoEPassword != ""

	if req.LANInterface != "" {
		cfg.LAN.Interface = req.LANInterface
	}
	if req.LANIPAddress != "" {
		if req.LANIPAddress != cfg.LAN.IPAddress {
			http.Error(w, "Complete first-run setup on the default LAN address; change the LAN address afterward using commit-confirmed configuration", http.StatusUnprocessableEntity)
			return
		}
		cfg.LAN.IPAddress = req.LANIPAddress
		cfg.LAN.CIDR = fmt.Sprintf("%s/24", req.LANIPAddress)
	}

	store := s.engine.GetStore()
	if store == nil {
		http.Error(w, "Initial setup store unavailable", http.StatusServiceUnavailable)
		return
	}
	txID := fmt.Sprintf("wizard-setup-%d", time.Now().UnixNano())
	tx, err := s.engine.ProcessInitialSetup(txID, cfg, func(applied config.SystemConfig) error {
		return store.CommitInitialSetup(applied, hashedPassword)
	})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusUnprocessableEntity
		if tx != nil && tx.CurrentState == apply.StateRecoveryRequired {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Wizard apply failed: %v", err),
			"tx":    redactTransaction(tx),
		})
		return
	}

	// The network revision and credential were committed in one SQLite
	// transaction by CommitInitialSetup. Publish the hash in memory only now.
	s.mu.Lock()
	s.adminHash = hashedPassword
	s.mu.Unlock()

	log.Printf("[AUTH] Wizard completed: admin password set, system configured from %s\n", r.RemoteAddr)
	s.appendAudit("system.setup_completed", auditActor(r.RemoteAddr), map[string]string{
		"wan_interface": cfg.WAN.Interface,
		"lan_interface": cfg.LAN.Interface,
	})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	// Session creation is deliberately after the atomic setup commit. If the
	// session store has a transient failure at this point, setup itself has still
	// succeeded. Report success and let the UI continue to the normal login flow
	// instead of falsely claiming that the router initialization failed.
	session := s.sessionMgr.CreateSession()
	if session == nil {
		log.Printf("[AUTH] Initial setup committed but durable session creation failed; explicit login required\n")
		s.appendAudit("system.setup_session_failed", auditActor(r.RemoteAddr), map[string]string{
			"result": "setup_committed_login_required",
		})
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":        true,
			"requires_login": true,
			"redirect_url":   fmt.Sprintf("https://%s", net.JoinHostPort(cfg.LAN.IPAddress, fmt.Sprint(cfg.System.HTTPSPort))),
			"tx":             redactTransaction(tx),
		})
		return
	}
	s.sessionMgr.SetSessionCookie(w, session)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"csrf_token":   session.CSRFToken,
		"redirect_url": fmt.Sprintf("https://%s", net.JoinHostPort(cfg.LAN.IPAddress, fmt.Sprint(cfg.System.HTTPSPort))),
		"tx":           redactTransaction(tx),
	})
}
