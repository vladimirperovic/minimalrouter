package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

// Server handles REST API requests for Minimal Router OS.
type Server struct {
	engine     *apply.Engine
	sessionMgr *auth.SessionManager
	rateLimiter *auth.RateLimiter
	adminHash  string // Argon2id hash of admin password
	mu         sync.RWMutex
}

// NewServer creates a new API server instance with authentication subsystem.
func NewServer(engine *apply.Engine) *Server {
	return &Server{
		engine:      engine,
		sessionMgr:  auth.NewSessionManager(),
		rateLimiter: auth.NewRateLimiter(5, 60*time.Second),
		adminHash:   "", // Empty until first-run wizard sets it
	}
}

// authMiddleware validates session cookie and CSRF token for protected endpoints.
// Returns 401 Unauthorized if session is invalid or expired.
// Returns 403 Forbidden if CSRF token mismatches on mutating methods.
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.sessionMgr.ValidateSession(r)
		if err != nil {
			log.Printf("[AUTH] Unauthorized request to %s from %s\n", r.URL.Path, r.RemoteAddr)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized or expired session"})
			return
		}

		// CSRF check for state-changing methods (POST, PUT, DELETE, PATCH)
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			csrfToken := r.Header.Get(auth.CSRFHeaderName)
			if csrfToken != sess.CSRFToken {
				log.Printf("[AUTH] CSRF token mismatch on %s %s from %s\n", r.Method, r.URL.Path, r.RemoteAddr)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{"error": "CSRF token mismatch"})
				return
			}
		}

		next(w, r)
	}
}

// RegisterRoutes attaches /api/v1 endpoints to the provided HTTP mux.
// Public endpoints: /auth/login, /setup/status, /setup/apply (first-run only)
// Protected endpoints: everything else (requires valid session)
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// ── Public endpoints (no auth required) ──
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/v1/setup/status", s.handleSetupStatus)

	// ── Protected endpoints (auth required) ──
	mux.HandleFunc("POST /api/v1/auth/logout", s.authMiddleware(s.handleLogout))
	mux.HandleFunc("GET /api/v1/auth/session", s.authMiddleware(s.handleGetSession))
	mux.HandleFunc("POST /api/v1/auth/change-password", s.authMiddleware(s.handleChangePassword))

	mux.HandleFunc("GET /api/v1/system", s.authMiddleware(s.handleGetSystem))
	mux.HandleFunc("GET /api/v1/system/diagnostics", s.authMiddleware(s.handleGetDiagnostics))
	mux.HandleFunc("GET /api/v1/config", s.authMiddleware(s.handleGetConfig))
	mux.HandleFunc("PUT /api/v1/config", s.authMiddleware(s.handleUpdateConfig))
	mux.HandleFunc("GET /api/v1/snapshots", s.authMiddleware(s.handleGetSnapshots))
	mux.HandleFunc("POST /api/v1/snapshots", s.authMiddleware(s.handleCreateSnapshot))
	mux.HandleFunc("POST /api/v1/snapshots/{id}/restore", s.authMiddleware(s.handleRestoreSnapshot))

	// ── Setup Wizard (first-run only, self-guarding) ──
	mux.HandleFunc("POST /api/v1/setup/apply", s.handleSetupApply)
}

// ── Authentication Handlers ──

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := r.RemoteAddr
	log.Printf("[AUTH] Login attempt from %s\n", ip)

	// Rate limiting per SECURITY.md §5
	if !s.rateLimiter.Allow(ip) {
		log.Printf("[AUTH] Rate limited login from %s\n", ip)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": "Too many login attempts. Try again later."})
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	hash := s.adminHash
	s.mu.RUnlock()

	// If no admin hash is set yet (first-run), reject login
	if hash == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error":         "System not configured. Run the setup wizard first.",
			"is_configured": "false",
		})
		return
	}

	match, err := auth.VerifyPassword(req.Password, hash)
	if err != nil || !match {
		log.Printf("[AUTH] Failed login from %s\n", ip)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid password"})
		return
	}

	session := s.sessionMgr.CreateSession()
	s.sessionMgr.SetSessionCookie(w, session)

	log.Printf("[AUTH] Successful login from %s (session: %s...)\n", ip, session.ID[:8])
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"csrf_token": session.CSRFToken,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	log.Printf("[AUTH] Logout from %s\n", r.RemoteAddr)
	s.sessionMgr.DestroySession(r, w)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Session invalidated",
	})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.sessionMgr.ValidateSession(r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"authenticated": true,
		"csrf_token":    sess.CSRFToken,
		"created_at":    sess.CreatedAt,
		"last_seen":     sess.LastSeen,
	})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	currentHash := s.adminHash
	s.mu.RUnlock()

	// Verify old password
	if currentHash != "" {
		match, err := auth.VerifyPassword(req.OldPassword, currentHash)
		if err != nil || !match {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Current password is incorrect"})
			return
		}
	}

	// Hash and store new password
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	s.mu.Lock()
	s.adminHash = newHash
	s.mu.Unlock()

	log.Printf("[AUTH] Admin password changed from %s\n", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Administrator password updated (Argon2id)",
	})
}

// ── Snapshot Handlers ──

func (s *Server) handleGetSnapshots(w http.ResponseWriter, r *http.Request) {
	log.Printf("[API] GET %s from %s\n", r.URL.Path, r.RemoteAddr)

	store := s.engine.GetStore()
	if store == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"snapshots": []interface{}{}})
		return
	}

	snapshots, err := store.ListSnapshots()
	if err != nil {
		log.Printf("[API] Failed to list snapshots: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"snapshots": []interface{}{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"snapshots": snapshots,
	})
}

func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	log.Printf("[API] POST %s from %s\n", r.URL.Path, r.RemoteAddr)

	store := s.engine.GetStore()
	if store == nil {
		http.Error(w, "Snapshot store unavailable", http.StatusInternalServerError)
		return
	}

	cfg := s.engine.GetCurrentConfig()
	snap, err := store.CreateSnapshot(cfg)
	if err != nil {
		log.Printf("[API] Failed to create snapshot: %v\n", err)
		http.Error(w, fmt.Sprintf("Snapshot creation failed: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("[API] Created snapshot %s (rev %d)\n", snap.ID, snap.Revision)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"snapshot": snap,
	})
}

func (s *Server) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	snapID := r.PathValue("id")
	log.Printf("[API] POST /api/v1/snapshots/%s/restore from %s\n", snapID, r.RemoteAddr)

	store := s.engine.GetStore()
	if store == nil {
		http.Error(w, "Snapshot store unavailable", http.StatusInternalServerError)
		return
	}

	snap, err := store.GetSnapshot(snapID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Snapshot not found: %s", snapID)})
		return
	}

	// Parse the stored config and apply it via transaction engine
	var restoredCfg config.SystemConfig
	if err := json.Unmarshal([]byte(snap.ConfigJSON), &restoredCfg); err != nil {
		http.Error(w, "Corrupted snapshot config", http.StatusInternalServerError)
		return
	}

	txID := fmt.Sprintf("restore-%s-%d", snapID, time.Now().UnixNano())
	tx, err := s.engine.ProcessTransaction(txID, restoredCfg)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Restore failed: %v", err),
		})
		return
	}

	log.Printf("[API] Restored snapshot %s (rev %d → %d)\n", snapID, snap.Revision, tx.Config.Revision)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"restored":     snapID,
		"new_revision": tx.Config.Revision,
		"timestamp":    time.Now().Unix(),
	})
}

// ── System Handlers ──

func (s *Server) handleGetSystem(w http.ResponseWriter, r *http.Request) {
	log.Printf("[API] GET %s from %s\n", r.URL.Path, r.RemoteAddr)
	cfg := s.engine.GetCurrentConfig()

	response := map[string]interface{}{
		"status":    "Connected",
		"version":   "v0.1-alpha",
		"hostname":  cfg.System.Hostname,
		"domain":    cfg.System.Domain,
		"wan_iface": cfg.WAN.Interface,
		"lan_ip":    cfg.LAN.IPAddress,
		"revision":  cfg.Revision,
		"timestamp": time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleGetDiagnostics(w http.ResponseWriter, r *http.Request) {
	log.Printf("[API] GET %s from %s\n", r.URL.Path, r.RemoteAddr)
	cfg := s.engine.GetCurrentConfig()
	data, err := telemetry.BuildDiagnosticBundle(cfg)
	if err != nil {
		http.Error(w, "Failed to build diagnostic bundle", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"minimalrouter-diagnostics.json\"")
	w.Write(data)
}

// ── Config Handlers ──

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	log.Printf("[API] GET %s from %s\n", r.URL.Path, r.RemoteAddr)
	cfg := s.engine.GetCurrentConfig()

	// SECURITY: Redact sensitive fields before returning per SECURITY.md §12, §15
	cfg.WAN.Password = "[REDACTED]"

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var newCfg config.SystemConfig
	if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}

	txID := fmt.Sprintf("tx-%d", time.Now().UnixNano())
	tx, err := s.engine.ProcessTransaction(txID, newCfg)
	if err != nil {
		log.Printf("[API] PUT %s - Transaction %s REJECTED: %v\n", r.URL.Path, txID, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
			"tx":    tx,
		})
		return
	}

	log.Printf("[API] PUT %s - Transaction %s COMMITTED (Rev: %d)\n", r.URL.Path, txID, tx.Config.Revision)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(tx)
}
