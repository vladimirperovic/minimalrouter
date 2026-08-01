package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/api"
	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/auth/persistent"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/firmware"
	"github.com/vladimirperovic/minimalrouter/internal/tlsutil"
)

func main() {
	// Memory tuning for embedded appliance: GC at 1.5x live heap, hard cap at 128 MB.
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(128 << 20)

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
	stopStorageMaintenance := startStorageMaintenance(store)
	defer stopStorageMaintenance()

	initialCfg, err := store.GetLatestConfig()
	if err != nil {
		log.Fatalf("Refusing startup because canonical configuration is unavailable: %v", err)
	}

	// Load persisted admin password hash from SQLite
	adminHash := ""
	if hash, err := store.GetAdminHash(); err == nil {
		adminHash = hash
		log.Println("[AUTH] Loaded persisted administrator password hash from SQLite store")
	} else if !errors.Is(err, sql.ErrNoRows) {
		log.Fatalf("Refusing startup because administrator state is unavailable: %v", err)
	}

	previewMode := os.Getenv("MINIMALROUTER_PREVIEW_MODE") == "1"
	previewHTTP := previewMode && os.Getenv("MINIMALROUTER_PREVIEW_HTTP") == "1"
	var engine *apply.Engine
	if previewMode {
		if runtime.GOOS != "darwin" {
			log.Fatal("MINIMALROUTER_PREVIEW_MODE is restricted to the macOS development build")
		}
		if os.Getenv("MINIMALROUTER_ALLOW_LOOPBACK_PREVIEW") != "1" {
			log.Fatal("macOS preview mode requires MINIMALROUTER_ALLOW_LOOPBACK_PREVIEW=1")
		}
		engine = apply.NewEngineWithClient(initialCfg, store, newPreviewApplyClient())
		log.Println("[PREVIEW] Linux changes are simulated; no host networking commands will run")
	} else {
		engine = apply.NewEngine(initialCfg, store)
	}
	// A factory-fresh appliance has only placeholder interface names. Start the
	// wizard before reconciliation so the administrator can select the real
	// interfaces (ens18, enp2s0, and similar). Configured appliances reconcile
	// before accepting mutations, but the read-only status plane remains online.
	if adminHash != "" {
		reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), 150*time.Second)
		if err := engine.Reconcile(reconcileCtx); err != nil {
			log.Printf("[RECOVERY] Canonical state reconciliation failed: %v", err)
		}
		reconcileCancel()
	}

	// Setup persistent session manager and rate limiter
	sessionMgr := persistent.NewPersistentSessionManagerWithSecureCookies(store, !previewHTTP)
	rateLimiter := persistent.NewPersistentRateLimiter(store, 5, 60*time.Second)
	globalRateLimiter := persistent.NewPersistentRateLimiter(store, 100, 60*time.Second)

	// Setup API server with persistent auth
	server := api.NewServerWithAuth(engine, sessionMgr, rateLimiter, adminHash, store)
	server.ConfigureGlobalLoginLimiter(globalRateLimiter)
	server.ConfigureLoopbackHTTPPreview(previewHTTP)
	stopGatewayMonitoring := configureGatewayMonitoring(server, absDir)
	defer stopGatewayMonitoring()
	const firmwareKeyPath = "/etc/minimalrouter/firmware-signing.pub"
	if trustedKey, err := firmware.LoadTrustedPublicKey(firmwareKeyPath); err == nil {
		server.ConfigureFirmwareTrust(trustedKey, "/var/lib/minimalrouter-update/staging")
		log.Printf("[SECURITY] Firmware verification enabled with pinned key %s", firmwareKeyPath)
	} else {
		log.Printf("[SECURITY] Firmware updates disabled: trusted key unavailable: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	server.RegisterGatewayRoutes(mux)
	if webDir := os.Getenv("MINIMALROUTER_WEB_DIR"); webDir != "" {
		mux.Handle("/", staticHandler(webDir))
		log.Printf("Serving dashboard from %s", webDir)
	}

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

	var certMu sync.Mutex
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
			certMu.Lock()
			defer certMu.Unlock()
			active := engine.GetCurrentConfig()
			activeCertPEM, activeKeyPEM, certErr := certMgr.EnsureCertificate(&active)
			if certErr != nil {
				return nil, certErr
			}
			activeCert, certErr := tls.X509KeyPair(activeCertPEM, activeKeyPEM)
			if certErr != nil {
				return nil, certErr
			}
			return &activeCert, nil
		},
		// Security hardening
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		},
	}

	port := initialCfg.System.HTTPSPort
	serverAddr := net.JoinHostPort("", strconv.Itoa(port))
	if previewHTTP {
		serverAddr = net.JoinHostPort("127.0.0.1", "8080")
	}

	log.Printf("routerd listening on firewall-confined management endpoint https://%s:%d/api/v1/\n", initialCfg.LAN.IPAddress, port)
	log.Printf("Certificate fingerprint displayed above - verify on first connect\n")

	srv := &http.Server{
		Addr:              serverAddr,
		Handler:           managementDestinationHandler(engine, storagePressureHandler(absDir, mux)),
		TLSConfig:         tlsConfig,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		// Configuration handlers have their own bounded operation contexts. A
		// server-wide write deadline would make a successful apply look failed to
		// the browser after the response socket is closed.
		WriteTimeout:   0,
		IdleTimeout:    15 * time.Second,
		MaxHeaderBytes: 32 << 10,
	}

	if previewHTTP {
		log.Printf("[PREVIEW] Dashboard available on loopback-only http://127.0.0.1:8080")
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalf("Preview server error: %v", err)
		}
		return
	}
	if err := srv.ListenAndServeTLS("", ""); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// managementDestinationHandler is a second boundary behind nftables. Even if
// a future firewall regression opens the TCP port, routerd refuses requests
// whose destination address is not an active LAN or WireGuard management IP.
func managementDestinationHandler(engine *apply.Engine, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localAddr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
		if !ok {
			http.NotFound(w, r)
			return
		}
		host, _, err := net.SplitHostPort(localAddr.String())
		if err != nil {
			http.NotFound(w, r)
			return
		}
		destination := net.ParseIP(host)
		if destination == nil {
			http.NotFound(w, r)
			return
		}

		allowed := make(map[string]struct{})
		allowedHosts := make(map[string]struct{})
		addConfigAddresses := func(cfg config.SystemConfig, includeLAN bool) {
			hostname := strings.ToLower(strings.TrimSpace(cfg.System.Hostname))
			domain := strings.ToLower(strings.Trim(strings.TrimSpace(cfg.System.Domain), "."))
			if hostname != "" {
				allowedHosts[hostname] = struct{}{}
				if domain != "" {
					allowedHosts[hostname+"."+domain] = struct{}{}
				}
			}
			if includeLAN {
				if ip := net.ParseIP(cfg.LAN.IPAddress); ip != nil {
					allowed[ip.String()] = struct{}{}
					allowedHosts[ip.String()] = struct{}{}
				}
			}
			if cfg.WireGuard.Enabled {
				if ip, _, parseErr := net.ParseCIDR(cfg.WireGuard.Address); parseErr == nil {
					allowed[ip.String()] = struct{}{}
					allowedHosts[ip.String()] = struct{}{}
				}
			}
		}

		current := engine.GetCurrentConfig()
		pending := engine.GetPendingTransaction()
		if pending == nil {
			addConfigAddresses(current, current.System.ManagementAccess != "wireguard_only")
		} else if pending.Config.System.ManagementAccess == "wireguard_only" {
			addConfigAddresses(pending.Config, false)
		} else {
			// LAN address changes are provisionally configured with both old
			// and candidate addresses until connectivity is confirmed.
			addConfigAddresses(current, true)
			addConfigAddresses(pending.Config, true)
		}

		if os.Getenv("MINIMALROUTER_ALLOW_LOOPBACK_PREVIEW") == "1" && destination.IsLoopback() {
			allowedHosts["127.0.0.1"] = struct{}{}
			allowedHosts["localhost"] = struct{}{}
			if !requestHostAllowed(r.Host, allowedHosts) {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if _, exists := allowed[destination.String()]; !exists {
			http.NotFound(w, r)
			return
		}
		if !requestHostAllowed(r.Host, allowedHosts) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestHostAllowed(rawHost string, allowed map[string]struct{}) bool {
	host := rawHost
	if parsed, _, err := net.SplitHostPort(rawHost); err == nil {
		host = parsed
	} else if strings.Contains(rawHost, ":") {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	_, ok := allowed[host]
	return ok
}

func staticHandler(root string) http.Handler {
	root = filepath.Clean(root)
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		relative := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")
		if relative == "." || relative == "" {
			relative = "index.html"
		}
		candidate := filepath.Join(root, relative)
		if !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
			http.NotFound(w, r)
			return
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			if filepath.Ext(relative) != "" {
				http.NotFound(w, r)
				return
			}
			candidate = filepath.Join(root, "index.html")
			if _, err := os.Stat(candidate); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; style-src-attr 'none'; script-src-attr 'none'; img-src 'self' data:; font-src 'self'; connect-src 'self'; worker-src 'none'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Strict-Transport-Security", "max-age=63072000")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if filepath.Base(candidate) == "index.html" {
			w.Header().Set("Cache-Control", "no-store")
		}
		http.ServeFile(w, r, candidate)
	})
}
