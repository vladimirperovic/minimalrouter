package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/api"
	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/auth/persistent"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/tlsutil"
)

func main() {
	log.Println("Starting Minimal Router OS routerd (unprivileged management plane)...")

	dataDir := os.Getenv("MINIMALROUTER_DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}

	absDir, _ := filepath.Abs(dataDir)
	log.Printf("Initializing configuration store at %s\n", absDir)

	store, err := config.NewFileStore(absDir)
	if err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}
	defer store.Close()

	initialCfg, err := store.GetLatestConfig()
	if err != nil {
		log.Printf("Warning: Could not read store, fallback to default: %v", err)
		initialCfg = config.DefaultConfig()
	}

	// Load persisted admin password hash from SQLite
	adminHash := ""
	if hash, err := store.GetAdminHash(); err == nil {
		adminHash = hash
		log.Println("[AUTH] Loaded persisted administrator password hash from SQLite store")
	}

	engine := apply.NewEngine(initialCfg, store)

	// Setup persistent session manager and rate limiter
	sessionMgr := persistent.NewPersistentSessionManager(store)
	rateLimiter := persistent.NewPersistentRateLimiter(store, 5, 60*time.Second)

	// Setup API server with persistent auth
	server := api.NewServerWithAuth(engine, sessionMgr, rateLimiter, adminHash, store)
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// TLS Certificate management
	certMgr := tlsutil.NewCertManager(absDir)
	certPEM, keyPEM, err := certMgr.EnsureCertificate(&initialCfg)
	if err != nil {
		log.Fatalf("Failed to setup TLS certificate: %v", err)
	}

	// Display certificate fingerprint for setup verification
	if fp, err := tlsutil.GetCertificateFingerprint(certPEM); err == nil {
		log.Printf("TLS Certificate Fingerprint (SHA256): %s", fp)
	}

	// Parse certificate and key for the server
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		log.Fatalf("Failed to load TLS key pair: %v", err)
	}

	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return &cert, nil
		},
		// Security hardening
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		},
	}

	port := 8443
	serverAddr := fmt.Sprintf(":%d", port)

	log.Printf("routerd listening on https://127.0.0.1:%d/api/v1/\n", port)
	log.Printf("Certificate fingerprint displayed above - verify on first connect\n")

	srv := &http.Server{
		Addr:      serverAddr,
		Handler:   mux,
		TLSConfig: tlsConfig,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if err := srv.ListenAndServeTLS("", ""); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}