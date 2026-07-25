package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/firmware"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

// Server handles REST API requests for Minimal Router OS.
type Server struct {
	engine         *apply.Engine
	sessionMgr     SessionManagerInterface
	rateLimiter    RateLimiterInterface
	globalLimiter  RateLimiterInterface
	adminHash      string              // Argon2id hash of admin password
	store          *config.SQLiteStore // for TOTP secret management
	firmwareKey    ed25519.PublicKey
	firmwareDir    string
	pendingTOTP    map[string]pendingTOTPEnrollment
	pendingImports map[string]pendingPfSenseImport
	totpReplay     map[[sha256.Size]byte]time.Time
	mu             sync.RWMutex
}

type pendingTOTPEnrollment struct {
	secret    string
	expiresAt time.Time
}

type pendingPfSenseImport struct {
	sessionID string
	config    config.SystemConfig
	expiresAt time.Time
}

// ConfigureFirmwareTrust installs the immutable update trust anchor and the
// root-controlled staging directory. An empty key leaves updates disabled.
func (s *Server) ConfigureFirmwareTrust(key ed25519.PublicKey, stagingDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.firmwareKey = append(ed25519.PublicKey(nil), key...)
	s.firmwareDir = stagingDir
}

// ConfigureGlobalLoginLimiter adds a router-wide limit in addition to the
// per-source limit, preventing distributed password guessing.
func (s *Server) ConfigureGlobalLoginLimiter(limiter RateLimiterInterface) {
	s.globalLimiter = limiter
}

// SessionManagerInterface defines the session management operations needed by the API.
type SessionManagerInterface interface {
	ValidateSession(r *http.Request) (*auth.Session, error)
	DestroySession(r *http.Request, w http.ResponseWriter)
	SetSessionCookie(w http.ResponseWriter, session *auth.Session)
	CreateSession() *auth.Session
	CreateSessionWithMode(readOnly bool) *auth.Session
	DestroyAllSessions() error
}

// RateLimiterInterface defines the rate limiting operations needed by the API.
type RateLimiterInterface interface {
	Allow(ip string) bool
}

type auditResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *auditResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func auditActor(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && host != "" {
		return host
	}
	if remoteAddr == "" {
		return "local"
	}
	return remoteAddr
}

func (s *Server) appendAudit(eventType, actor string, details map[string]string) {
	store := s.store
	if store == nil && s.engine != nil {
		store = s.engine.GetStore()
	}
	if store == nil {
		return
	}
	if err := store.AppendAuditEvent(eventType, actor, details); err != nil {
		log.Printf("[AUDIT] failed to persist %s: %v", eventType, err)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			return fmt.Errorf("content type must be application/json")
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request must contain exactly one JSON object")
	}
	return nil
}

// NewServer creates a new API server instance with in-memory authentication subsystem.
func NewServer(engine *apply.Engine) *Server {
	return &Server{
		engine:         engine,
		sessionMgr:     auth.NewSessionManager(),
		rateLimiter:    auth.NewRateLimiter(5, 60*time.Second),
		globalLimiter:  auth.NewRateLimiter(100, 60*time.Second),
		adminHash:      "",
		pendingTOTP:    make(map[string]pendingTOTPEnrollment),
		pendingImports: make(map[string]pendingPfSenseImport),
		totpReplay:     make(map[[sha256.Size]byte]time.Time),
	}
}

// NewServerWithAuth creates a new API server instance with persistent authentication subsystem.
func NewServerWithAuth(engine *apply.Engine, sessionMgr SessionManagerInterface, rateLimiter RateLimiterInterface, adminHash string, store *config.SQLiteStore) *Server {
	return &Server{
		engine:         engine,
		sessionMgr:     sessionMgr,
		rateLimiter:    rateLimiter,
		globalLimiter:  auth.NewRateLimiter(100, 60*time.Second),
		adminHash:      adminHash,
		store:          store,
		pendingTOTP:    make(map[string]pendingTOTPEnrollment),
		pendingImports: make(map[string]pendingPfSenseImport),
		totpReplay:     make(map[[sha256.Size]byte]time.Time),
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
			s.appendAudit("auth.unauthorized", auditActor(r.RemoteAddr), map[string]string{
				"method": r.Method,
				"path":   r.URL.Path,
			})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized or expired session"})
			return
		}

		// CSRF check for state-changing methods (POST, PUT, DELETE, PATCH)
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if sess.ReadOnly && r.URL.Path != "/api/v1/auth/logout" {
				log.Printf("[AUTH] Read-only session rejected mutation %s %s\n", r.Method, r.URL.Path)
				s.appendAudit("auth.read_only_rejected", auditActor(r.RemoteAddr), map[string]string{
					"method": r.Method,
					"path":   r.URL.Path,
				})
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{"error": "Read-only session cannot change router state"})
				return
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				s.appendAudit("auth.cross_site_rejected", auditActor(r.RemoteAddr), map[string]string{
					"method": r.Method,
					"path":   r.URL.Path,
				})
				http.Error(w, "Cross-site request rejected", http.StatusForbidden)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, parseErr := url.Parse(origin)
				if parseErr != nil || parsed.Scheme != "https" || parsed.Host != r.Host {
					s.appendAudit("auth.origin_rejected", auditActor(r.RemoteAddr), map[string]string{
						"method": r.Method,
						"path":   r.URL.Path,
					})
					http.Error(w, "Origin rejected", http.StatusForbidden)
					return
				}
			}
			csrfToken := r.Header.Get(auth.CSRFHeaderName)
			if subtle.ConstantTimeCompare([]byte(csrfToken), []byte(sess.CSRFToken)) != 1 {
				log.Printf("[AUTH] CSRF token mismatch on %s %s from %s\n", r.Method, r.URL.Path, r.RemoteAddr)
				s.appendAudit("auth.csrf_rejected", auditActor(r.RemoteAddr), map[string]string{
					"method": r.Method,
					"path":   r.URL.Path,
				})
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{"error": "CSRF token mismatch"})
				return
			}
		}

		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next(w, r)
			return
		}
		recorder := &auditResponseWriter{ResponseWriter: w}
		next(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		s.appendAudit("api.mutation", auditActor(r.RemoteAddr), map[string]string{
			"method": r.Method,
			"path":   r.URL.Path,
			"status": strconv.Itoa(status),
		})
	}
}

// securityHeadersMiddleware sets strict web security headers on all API responses per SECURITY.md §6.
func (s *Server) securityHeadersMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

// RegisterRoutes attaches /api/v1 endpoints to the provided HTTP mux.
// Public endpoints: /auth/login, /setup/status, /setup/apply (first-run only)
// Protected endpoints: everything else (requires valid session)
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	sh := s.securityHeadersMiddleware

	// ── Public endpoints (no auth required) ──
	mux.HandleFunc("POST /api/v1/auth/login", sh(s.handleLogin))
	mux.HandleFunc("GET /api/v1/setup/status", sh(s.handleSetupStatus))

	// ── Protected endpoints (auth required) ──
	mux.HandleFunc("POST /api/v1/auth/logout", sh(s.authMiddleware(s.handleLogout)))
	mux.HandleFunc("GET /api/v1/auth/session", sh(s.authMiddleware(s.handleGetSession)))
	mux.HandleFunc("POST /api/v1/auth/change-password", sh(s.authMiddleware(s.handleChangePassword)))

	// TOTP endpoints (auth required)
	mux.HandleFunc("POST /api/v1/auth/totp/enable", sh(s.authMiddleware(s.handleTOTPEnable)))
	mux.HandleFunc("POST /api/v1/auth/totp/enroll", sh(s.authMiddleware(s.handleTOTPQR)))
	mux.HandleFunc("POST /api/v1/auth/totp/disable", sh(s.authMiddleware(s.handleTOTPDisable)))

	mux.HandleFunc("GET /api/v1/system", sh(s.authMiddleware(s.handleGetSystem)))
	mux.HandleFunc("GET /api/v1/system/diagnostics", sh(s.authMiddleware(s.handleGetDiagnostics)))
	mux.HandleFunc("GET /api/v1/audit/events", sh(s.authMiddleware(s.handleGetAuditEvents)))
	mux.HandleFunc("GET /api/v1/config", sh(s.authMiddleware(s.handleGetConfig)))
	mux.HandleFunc("PUT /api/v1/config", sh(s.authMiddleware(s.handleUpdateConfig)))
	mux.HandleFunc("POST /api/v1/wireguard/peers", sh(s.authMiddleware(s.handleProvisionWireGuardPeer)))
	mux.HandleFunc("GET /api/v1/transactions/pending", sh(s.authMiddleware(s.handleGetPendingTransaction)))
	mux.HandleFunc("POST /api/v1/transactions/{id}/confirm", sh(s.authMiddleware(s.handleConfirmTransaction)))
	mux.HandleFunc("GET /api/v1/snapshots", sh(s.authMiddleware(s.handleGetSnapshots)))
	mux.HandleFunc("POST /api/v1/snapshots", sh(s.authMiddleware(s.handleCreateSnapshot)))
	mux.HandleFunc("POST /api/v1/snapshots/{id}/restore", sh(s.authMiddleware(s.handleRestoreSnapshot)))
	mux.HandleFunc("POST /api/v1/import/pfsense/preview", sh(s.authMiddleware(s.handlePfSenseImportPreview)))
	mux.HandleFunc("POST /api/v1/import/pfsense/{id}/apply", sh(s.authMiddleware(s.handlePfSenseImportApply)))

	// ── Authenticated encrypted backup and restore ──
	mux.HandleFunc("POST /api/v1/backup/export", sh(s.authMiddleware(s.handleBackupExport)))
	mux.HandleFunc("POST /api/v1/backup/import/preview", sh(s.authMiddleware(s.handleBackupImportPreview)))
	mux.HandleFunc("POST /api/v1/import/backup/{id}/apply", sh(s.authMiddleware(s.handleBackupImportApply)))

	// ── Firmware Verification (P1) ──
	mux.HandleFunc("POST /api/v1/firmware/verify", sh(s.authMiddleware(s.handleFirmwareVerify)))

	// ── Setup Wizard (first-run only, self-guarding) ──
	mux.HandleFunc("POST /api/v1/setup/apply", sh(s.handleSetupApply))
}

// ── Authentication Handlers ──

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	log.Printf("[AUTH] Login attempt from %s\n", ip)

	// Rate limiting per SECURITY.md §5
	if !s.rateLimiter.Allow(ip) || (s.globalLimiter != nil && !s.globalLimiter.Allow("all-login-sources")) {
		log.Printf("[AUTH] Rate limited login from %s\n", ip)
		s.appendAudit("auth.login_rate_limited", ip, map[string]string{"result": "rejected"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": "Too many login attempts. Try again later."})
		return
	}

	var req struct {
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
		ReadOnly bool   `json:"read_only"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
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
		s.appendAudit("auth.login_failed", ip, map[string]string{"result": "invalid_credentials"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid credentials"})
		return
	}

	// Check if TOTP is configured
	if s.store != nil {
		totpSecret, err := s.store.GetAdminTOTPSecret()
		if err == nil && totpSecret != "" {
			// TOTP is configured - require code
			if req.TOTPCode == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{
					"error":         "TOTP code required",
					"totp_required": "true",
				})
				return
			}
			if !auth.ValidateTOTP(totpSecret, req.TOTPCode) || !s.consumeTOTP(totpSecret, req.TOTPCode) {
				log.Printf("[AUTH] Invalid TOTP code from %s\n", ip)
				s.appendAudit("auth.login_failed", ip, map[string]string{"result": "invalid_totp"})
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "Invalid credentials"})
				return
			}
		}
	}

	session := s.sessionMgr.CreateSessionWithMode(req.ReadOnly)
	if session == nil {
		http.Error(w, "Could not create a durable session", http.StatusInternalServerError)
		return
	}
	s.sessionMgr.SetSessionCookie(w, session)

	log.Printf("[AUTH] Successful login from %s (session: %s...)\n", ip, session.ID[:8])
	s.appendAudit("auth.login_succeeded", ip, map[string]string{
		"mode": map[bool]string{true: "read_only", false: "administrator"}[session.ReadOnly],
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"csrf_token": session.CSRFToken,
		"read_only":  session.ReadOnly,
	})
}

func (s *Server) consumeTOTP(secret, code string) bool {
	key := sha256.Sum256([]byte(secret + "\x00" + code))
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for used, expires := range s.totpReplay {
		if now.After(expires) {
			delete(s.totpReplay, used)
		}
	}
	if _, replayed := s.totpReplay[key]; replayed {
		return false
	}
	s.totpReplay[key] = now.Add(2 * time.Minute)
	return true
}

// ── Firmware Verification Handler (P1) ──

func (s *Server) handleFirmwareVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ManifestB64 string `json:"manifest_b64"` // base64-encoded JSON manifest
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Decode manifest
	manifestJSON, err := base64.StdEncoding.DecodeString(req.ManifestB64)
	if err != nil {
		http.Error(w, "Invalid manifest base64", http.StatusBadRequest)
		return
	}

	var manifest firmware.FirmwareManifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		http.Error(w, "Invalid manifest JSON", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	trustedKey := append(ed25519.PublicKey(nil), s.firmwareKey...)
	stagingDir := s.firmwareDir
	s.mu.RUnlock()
	if len(trustedKey) != ed25519.PublicKeySize || stagingDir == "" {
		http.Error(w, "Firmware updates are disabled: no trusted signing key is installed", http.StatusServiceUnavailable)
		return
	}
	if err := firmware.VerifyFirmware(stagingDir, &manifest, trustedKey); err != nil {
		log.Printf("[SECURITY] Firmware verification rejected: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "valid": false})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"valid":       true,
		"version":     manifest.Version,
		"files_count": len(manifest.Files),
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
		"read_only":     sess.ReadOnly,
		"created_at":    sess.CreatedAt,
		"last_seen":     sess.LastSeen,
	})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
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

	if store := s.engine.GetStore(); store != nil {
		if err := store.SetAdminHash(newHash); err != nil {
			http.Error(w, "Failed to persist password", http.StatusInternalServerError)
			return
		}
	}
	s.mu.Lock()
	s.adminHash = newHash
	s.mu.Unlock()
	if err := s.sessionMgr.DestroyAllSessions(); err != nil {
		http.Error(w, "Password changed but session revocation failed", http.StatusInternalServerError)
		return
	}
	s.sessionMgr.DestroySession(r, w)

	log.Printf("[AUTH] Admin password changed from %s\n", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Administrator password updated (Argon2id)",
	})
}

// ── TOTP Handlers (2FA) ──

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	sess, _ := s.sessionMgr.ValidateSession(r)
	s.mu.Lock()
	pending, ok := s.pendingTOTP[sess.ID]
	if ok && time.Now().After(pending.expiresAt) {
		delete(s.pendingTOTP, sess.ID)
		ok = false
	}
	s.mu.Unlock()
	if !ok || !auth.ValidateTOTP(pending.secret, req.Code) || !s.consumeTOTP(pending.secret, req.Code) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid TOTP code"})
		return
	}
	if s.store == nil || s.store.SetAdminTOTPSecret(pending.secret) != nil {
		http.Error(w, "Failed to persist TOTP configuration", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	delete(s.pendingTOTP, sess.ID)
	s.mu.Unlock()
	if err := s.sessionMgr.DestroyAllSessions(); err != nil {
		http.Error(w, "TOTP enabled but session revocation failed", http.StatusInternalServerError)
		return
	}
	s.sessionMgr.DestroySession(r, w)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "TOTP enabled successfully",
	})
}

func (s *Server) handleTOTPQR(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "TOTP store unavailable", http.StatusServiceUnavailable)
		return
	}
	// Check if TOTP is already enabled
	secret, err := s.store.GetAdminTOTPSecret()
	if err != nil {
		http.Error(w, "Failed to check TOTP status", http.StatusInternalServerError)
		return
	}

	if secret != "" {
		http.Error(w, "TOTP is already enabled", http.StatusConflict)
		return
	}

	newSecret, err := auth.GenerateTOTPSecret()
	if err != nil {
		http.Error(w, "Failed to generate TOTP secret", http.StatusInternalServerError)
		return
	}
	sess, _ := s.sessionMgr.ValidateSession(r)
	s.mu.Lock()
	s.pendingTOTP[sess.ID] = pendingTOTPEnrollment{
		secret: newSecret, expiresAt: time.Now().Add(10 * time.Minute),
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled": false,
		"secret":  newSecret,
		"qr_uri":  auth.BuildTOTPURI("admin", newSecret),
	})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Verify TOTP code before disabling
	valid, err := auth.VerifyTOTP(s.store, req.Code)
	secret, secretErr := s.store.GetAdminTOTPSecret()
	if err != nil || secretErr != nil || !valid || !s.consumeTOTP(secret, req.Code) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid TOTP code"})
		return
	}

	// Disable TOTP
	if err := auth.DisableTOTP(s.store); err != nil {
		http.Error(w, "Failed to disable TOTP", http.StatusInternalServerError)
		return
	}
	if err := s.sessionMgr.DestroyAllSessions(); err != nil {
		http.Error(w, "TOTP disabled but session revocation failed", http.StatusInternalServerError)
		return
	}
	s.sessionMgr.DestroySession(r, w)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "TOTP disabled successfully",
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
	// Snapshot revisions describe history; optimistic concurrency must compare
	// against the currently active revision before creating a new revision.
	restoredCfg.Revision = s.engine.GetCurrentConfig().Revision

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
	if tx.CurrentState == apply.StateAwaitingConfirmation {
		w.WriteHeader(http.StatusAccepted)
	}
	json.NewEncoder(w).Encode(redactTransaction(tx))
}

// ── pfSense Migration ──

func (s *Server) handlePfSenseImportPreview(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/xml" && mediaType != "text/xml") {
		http.Error(w, "Content-Type must be application/xml", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		http.Error(w, "pfSense configuration is too large", http.StatusRequestEntityTooLarge)
		return
	}
	report, err := config.ImportPfSenseXMLWithMapping(body, config.PfSenseInterfaceMapping{
		WAN: r.URL.Query().Get("wan"),
		LAN: r.URL.Query().Get("lan"),
	})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	session, err := s.sessionMgr.ValidateSession(r)
	if err != nil {
		http.Error(w, "Authenticated session required", http.StatusUnauthorized)
		return
	}
	idBytes := make([]byte, 24)
	if _, err := rand.Read(idBytes); err != nil {
		http.Error(w, "Could not create import preview", http.StatusInternalServerError)
		return
	}
	importID := base64.RawURLEncoding.EncodeToString(idBytes)
	current := s.engine.GetCurrentConfig()
	report.Config.Revision = current.Revision

	s.mu.Lock()
	now := time.Now()
	for id, pending := range s.pendingImports {
		if now.After(pending.expiresAt) {
			delete(s.pendingImports, id)
		}
	}
	s.pendingImports[importID] = pendingPfSenseImport{
		sessionID: session.ID,
		config:    report.Config,
		expiresAt: now.Add(10 * time.Minute),
	}
	s.mu.Unlock()

	// The browser receives a write-only placeholder, never the imported PPPoE
	// credential. The full candidate remains server-side until apply/expiry.
	report.Config = redactConfig(report.Config)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"import_id":          importID,
		"expires_in_seconds": 600,
		"report":             report,
	})
}

func (s *Server) handlePfSenseImportApply(w http.ResponseWriter, r *http.Request) {
	session, err := s.sessionMgr.ValidateSession(r)
	if err != nil {
		http.Error(w, "Authenticated session required", http.StatusUnauthorized)
		return
	}
	importID := r.PathValue("id")
	s.mu.Lock()
	pending, ok := s.pendingImports[importID]
	if ok {
		delete(s.pendingImports, importID)
	}
	s.mu.Unlock()
	if !ok || pending.sessionID != session.ID || time.Now().After(pending.expiresAt) {
		http.Error(w, "Import preview not found or expired", http.StatusNotFound)
		return
	}

	pending.config.Revision = s.engine.GetCurrentConfig().Revision
	tx, err := s.engine.ProcessTransaction("pfsense-import-"+importID, pending.config)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error(), "tx": redactTransaction(tx)})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if tx.CurrentState == apply.StateAwaitingConfirmation {
		w.WriteHeader(http.StatusAccepted)
	}
	json.NewEncoder(w).Encode(redactTransaction(tx))
}

// ── System Handlers ──

func (s *Server) handleGetSystem(w http.ResponseWriter, r *http.Request) {
	log.Printf("[API] GET %s from %s\n", r.URL.Path, r.RemoteAddr)
	cfg := s.engine.GetCurrentConfig()
	dataDir := os.Getenv("MINIMALROUTER_DATA_DIR")
	if dataDir == "" {
		dataDir = "/var/lib/minimalrouter"
	}
	runtimeStatus := telemetry.RuntimeSnapshot(cfg.WAN.Interface, dataDir)
	connectionStatus := "Disconnected"
	if cfg.WAN.Enabled && runtimeStatus.WANConnected {
		connectionStatus = "Connected"
	}
	s.mu.RLock()
	updateTrustConfigured := len(s.firmwareKey) == ed25519.PublicKeySize
	s.mu.RUnlock()

	response := map[string]interface{}{
		"status":                  connectionStatus,
		"version":                 "v0.1-alpha",
		"hostname":                cfg.System.Hostname,
		"domain":                  cfg.System.Domain,
		"wan_iface":               cfg.WAN.Interface,
		"wan_enabled":             cfg.WAN.Enabled,
		"lan_ip":                  cfg.LAN.IPAddress,
		"mtu":                     cfg.WAN.MTU,
		"revision":                cfg.Revision,
		"runtime":                 runtimeStatus,
		"update_trust_configured": updateTrustConfigured,
		"timestamp":               time.Now().Unix(),
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

	cfg = redactConfig(cfg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var newCfg config.SystemConfig
	if err := decodeJSON(w, r, &newCfg); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}
	current := s.engine.GetCurrentConfig()
	if newCfg.WAN.Password == "[REDACTED]" {
		newCfg.WAN.Password = current.WAN.Password
	}
	if newCfg.WireGuard.PrivateKey == "[REDACTED]" {
		newCfg.WireGuard.PrivateKey = current.WireGuard.PrivateKey
	}
	for i := range newCfg.WireGuard.Peers {
		if newCfg.WireGuard.Peers[i].PresharedKey != "[REDACTED]" {
			continue
		}
		for _, existing := range current.WireGuard.Peers {
			if existing.ID == newCfg.WireGuard.Peers[i].ID {
				newCfg.WireGuard.Peers[i].PresharedKey = existing.PresharedKey
				break
			}
		}
	}
	if newCfg.Cloudflare.APIToken == "[REDACTED]" {
		newCfg.Cloudflare.APIToken = current.Cloudflare.APIToken
	}
	if newCfg.Cloudflare.TunnelToken == "[REDACTED]" {
		newCfg.Cloudflare.TunnelToken = current.Cloudflare.TunnelToken
	}
	if newCfg.SquidProxy.Password == "[REDACTED]" {
		newCfg.SquidProxy.Password = current.SquidProxy.Password
	}
	if newCfg.WiFi.Passphrase == "[REDACTED]" {
		newCfg.WiFi.Passphrase = current.WiFi.Passphrase
	}

	txID := fmt.Sprintf("tx-%d", time.Now().UnixNano())
	tx, err := s.engine.ProcessTransaction(txID, newCfg)
	if err != nil {
		log.Printf("[API] PUT %s - Transaction %s REJECTED: %v\n", r.URL.Path, txID, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
			"tx":    redactTransaction(tx),
		})
		return
	}

	log.Printf("[API] PUT %s - Transaction %s %s (Rev: %d)\n", r.URL.Path, txID, tx.CurrentState, tx.Config.Revision)
	w.Header().Set("Content-Type", "application/json")
	if tx.CurrentState == apply.StateAwaitingConfirmation {
		w.WriteHeader(http.StatusAccepted)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	json.NewEncoder(w).Encode(redactTransaction(tx))
}

func (s *Server) handleConfirmTransaction(w http.ResponseWriter, r *http.Request) {
	pending := s.engine.GetPendingTransaction()
	if pending == nil || pending.ID != r.PathValue("id") {
		http.Error(w, "Transaction confirmation failed", http.StatusConflict)
		return
	}
	if pending.Config.System.ManagementAccess == "wireguard_only" {
		remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			remoteIP = r.RemoteAddr
		}
		peerIP := net.ParseIP(remoteIP)
		_, wgNetwork, parseErr := net.ParseCIDR(pending.Config.WireGuard.Address)
		if parseErr != nil || peerIP == nil || !wgNetwork.Contains(peerIP) {
			http.Error(w, "WireGuard connectivity must be used to confirm this change", http.StatusForbidden)
			return
		}
	}
	tx, err := s.engine.ConfirmTransaction(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Transaction confirmation failed", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(redactTransaction(tx))
}

func (s *Server) handleGetPendingTransaction(w http.ResponseWriter, _ *http.Request) {
	tx := s.engine.GetPendingTransaction()
	w.Header().Set("Content-Type", "application/json")
	if tx == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"pending": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pending":               true,
		"id":                    tx.ID,
		"state":                 tx.CurrentState,
		"confirmation_deadline": tx.ConfirmationDeadline,
		"management_access":     tx.Config.System.ManagementAccess,
	})
}

// ── Backup Encryption Handlers (P1) ──

func (s *Server) verifyCurrentPassword(password string) bool {
	s.mu.RLock()
	hash := s.adminHash
	s.mu.RUnlock()
	match, err := auth.VerifyPassword(password, hash)
	return err == nil && match
}

func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword  string `json:"current_password"`
		BackupPassphrase string `json:"backup_passphrase"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if !s.verifyCurrentPassword(req.CurrentPassword) {
		http.Error(w, "Current administrator password is incorrect", http.StatusUnauthorized)
		return
	}
	encrypted, err := config.EncryptConfigBackup(s.engine.GetCurrentConfig(), req.BackupPassphrase)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.minimalrouter.backup+json")
	w.Header().Set("Content-Disposition", `attachment; filename="minimalrouter-backup.mrbak"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(encrypted)
}

func (s *Server) handleBackupImportPreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 18<<20)
	if err := r.ParseMultipartForm(18 << 20); err != nil {
		http.Error(w, "Invalid or oversized backup upload", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	currentPassword := r.FormValue("current_password")
	passphrase := r.FormValue("backup_passphrase")
	if !s.verifyCurrentPassword(currentPassword) {
		http.Error(w, "Current administrator password is incorrect", http.StatusUnauthorized)
		return
	}
	file, _, err := r.FormFile("backup")
	if err != nil {
		http.Error(w, "Encrypted backup file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	if err != nil || len(data) > 16<<20 {
		http.Error(w, "Encrypted backup is too large", http.StatusRequestEntityTooLarge)
		return
	}
	candidate, err := config.DecryptConfigBackup(data, passphrase)
	if err != nil {
		http.Error(w, "Backup could not be authenticated or validated", http.StatusUnprocessableEntity)
		return
	}
	session, err := s.sessionMgr.ValidateSession(r)
	if err != nil {
		http.Error(w, "Authenticated session required", http.StatusUnauthorized)
		return
	}
	idBytes := make([]byte, 24)
	if _, err := rand.Read(idBytes); err != nil {
		http.Error(w, "Could not create restore preview", http.StatusInternalServerError)
		return
	}
	importID := base64.RawURLEncoding.EncodeToString(idBytes)
	candidate.Revision = s.engine.GetCurrentConfig().Revision
	s.mu.Lock()
	s.pendingImports[importID] = pendingPfSenseImport{
		sessionID: session.ID,
		config:    candidate,
		expiresAt: time.Now().Add(10 * time.Minute),
	}
	s.mu.Unlock()

	candidate = redactConfig(candidate)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"import_id":          importID,
		"expires_in_seconds": 600,
		"candidate":          candidate,
	})
}

func (s *Server) handleBackupImportApply(w http.ResponseWriter, r *http.Request) {
	session, err := s.sessionMgr.ValidateSession(r)
	if err != nil {
		http.Error(w, "Authenticated session required", http.StatusUnauthorized)
		return
	}
	importID := r.PathValue("id")
	s.mu.Lock()
	pending, ok := s.pendingImports[importID]
	if ok {
		delete(s.pendingImports, importID)
	}
	s.mu.Unlock()
	if !ok || pending.sessionID != session.ID || time.Now().After(pending.expiresAt) {
		http.Error(w, "Restore preview not found or expired", http.StatusNotFound)
		return
	}
	pending.config.Revision = s.engine.GetCurrentConfig().Revision
	tx, err := s.engine.ProcessTransaction("backup-restore-"+importID, pending.config)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error(), "tx": redactTransaction(tx)})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if tx.CurrentState == apply.StateAwaitingConfirmation {
		w.WriteHeader(http.StatusAccepted)
	}
	json.NewEncoder(w).Encode(redactTransaction(tx))
}

// Legacy raw-key helpers are retained as internal compatibility code but are
// intentionally not registered as HTTP routes.
func (s *Server) handleBackupEncrypt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SnapshotID string `json:"snapshot_id"`
		KeyB64     string `json:"key_b64"` // base64-encoded 32-byte key
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	store := s.engine.GetStore()
	if store == nil {
		http.Error(w, "Snapshot store unavailable", http.StatusInternalServerError)
		return
	}

	snap, err := store.GetSnapshot(req.SnapshotID)
	if err != nil {
		http.Error(w, "Snapshot not found", http.StatusNotFound)
		return
	}

	// Marshal snapshot to JSON
	snapJSON, err := json.Marshal(snap)
	if err != nil {
		http.Error(w, "Failed to marshal snapshot", http.StatusInternalServerError)
		return
	}

	// Decode key
	key, err := base64.StdEncoding.DecodeString(req.KeyB64)
	if err != nil || len(key) != 32 {
		http.Error(w, "Invalid key (must be 32 bytes base64)", http.StatusBadRequest)
		return
	}

	// Encrypt using AES-GCM
	block, err := aes.NewCipher(key)
	if err != nil {
		http.Error(w, "Cipher error", http.StatusInternalServerError)
		return
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		http.Error(w, "GCM error", http.StatusInternalServerError)
		return
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		http.Error(w, "Nonce generation failed", http.StatusInternalServerError)
		return
	}

	ciphertext := gcm.Seal(nonce, nonce, snapJSON, nil)
	encoded := base64.StdEncoding.EncodeToString(ciphertext)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"encrypted":   encoded,
		"snapshot_id": req.SnapshotID,
		"algorithm":   "AES-256-GCM",
	})
}

func (s *Server) handleBackupDecrypt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EncryptedB64 string `json:"encrypted_b64"`
		KeyB64       string `json:"key_b64"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	ciphertext, err := base64.StdEncoding.DecodeString(req.EncryptedB64)
	if err != nil {
		http.Error(w, "Invalid base64", http.StatusBadRequest)
		return
	}

	key, err := base64.StdEncoding.DecodeString(req.KeyB64)
	if err != nil || len(key) != 32 {
		http.Error(w, "Invalid key (must be 32 bytes base64)", http.StatusBadRequest)
		return
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		http.Error(w, "Cipher error", http.StatusInternalServerError)
		return
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		http.Error(w, "GCM error", http.StatusInternalServerError)
		return
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		http.Error(w, "Ciphertext too short", http.StatusBadRequest)
		return
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		http.Error(w, "Decryption failed (wrong key?)", http.StatusUnauthorized)
		return
	}

	// Parse the decrypted snapshot
	var snap config.Snapshot
	if err := json.Unmarshal(plaintext, &snap); err != nil {
		http.Error(w, "Corrupted snapshot data", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"snapshot": snap,
	})
}
