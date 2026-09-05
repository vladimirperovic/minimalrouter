package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/api"
	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/auth/persistent"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/firmware"
	"github.com/vladimirperovic/minimalrouter/internal/tlsutil"
)

func main() {
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
	if adminHash != "" {
		if startupRuntimeVerified(initialCfg) {
			// router-applyd already restored and verified this exact last-good
			// configuration during the same cold boot. Avoid immediately rewriting
			// every artifact, nftables policy, LAN state and QoS a second time.
			// Missing/stale/mismatched one-shot proof falls through to the full
			// canonical reconciliation below.
			log.Printf("[BOOT] privileged startup runtime already verified canonical revision %d; duplicate reconcile skipped", initialCfg.Revision)
		} else {
			// The privileged apply budget is privilegedApplyTimeout (2m) x
			// privilegedApplyAttempts (2) = 4m worst case. A 150s budget here used to
			// cancel the second attempt mid-flight and turn a recoverable transport
			// retry into a fatal start. Keep this above the helper budget, and keep
			// routerd.initd start_post above this value.
			reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), apply.ReconcileBudget)
			if err := engine.Reconcile(reconcileCtx); err != nil {
				reconcileCancel()
				// Serving the API after a failed canonical reconcile can create a
				// management lockout: SQLite may contain a recovery LAN address while
				// the kernel/helper still runs the old last-good address. OpenRC
				// supervises routerd, so fail this start attempt and retry instead of
				// exposing a management process whose destination policy describes a
				// runtime that was never proven active.
				log.Fatalf("Refusing startup because canonical runtime reconciliation failed: %v", err)
			}
			reconcileCancel()
		}
	}

	sessionMgr := persistent.NewPersistentSessionManagerWithSecureCookies(store, !previewHTTP)
	rateLimiter := persistent.NewPersistentRateLimiter(store, 5, 60*time.Second)
	globalRateLimiter := persistent.NewPersistentRateLimiter(store, 100, 60*time.Second)

	server := api.NewServerWithAuth(engine, sessionMgr, rateLimiter, adminHash, store)
	server.ConfigureGlobalLoginLimiter(globalRateLimiter)
	server.ConfigureLoopbackHTTPPreview(previewHTTP)
	stopGatewayMonitoring := configureGatewayMonitoring(server, absDir)
	defer stopGatewayMonitoring()
	stopAccounting := configureAccounting(server, engine, absDir)
	defer stopAccounting()
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
	server.RegisterHealthRoutes(mux)
	server.RegisterAccountingRoutes(mux)
	if webDir := os.Getenv("MINIMALROUTER_WEB_DIR"); webDir != "" {
		mux.Handle("/", staticHandler(webDir))
		log.Printf("Serving dashboard from %s", webDir)
	}

	certMgr := tlsutil.NewCertManager(absDir)
	certPEM, keyPEM, err := certMgr.EnsureCertificate(&initialCfg)
	if err != nil {
		log.Fatalf("Failed to setup TLS certificate: %v", err)
	}
	if fp, err := tlsutil.GetCertificateFingerprint(certPEM); err == nil {
		log.Printf("TLS Certificate Fingerprint (SHA256): %s", fp)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		log.Fatalf("Failed to load TLS key pair: %v", err)
	}

	// EnsureCertificateWithAdditionalSANs reads and parses PEM material from
	// disk. Doing that on every ClientHello made each TLS handshake pay disk I/O
	// plus an X509 keypair parse, which high-frequency dashboard polling turns
	// into a constant cost. Cache the result and rebuild only when the inputs
	// that go into the SAN list actually change.
	var (
		certMu        sync.Mutex
		cachedCert    *tls.Certificate
		cachedCertKey string
		// The cache key is derived from configuration only, so nothing in it
		// changes as time passes. Keeping the parsed validity window lets a
		// cache hit be re-checked for expiry (and for a not-yet-valid window
		// after a clock correction) without re-reading and re-parsing PEM on
		// every handshake.
		cachedNotBefore time.Time
		cachedNotAfter  time.Time
	)
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
			certMu.Lock()
			defer certMu.Unlock()

			active := engine.GetCurrentConfig()
			additionalIPs := make([]net.IP, 0, 2)
			additionalDNS := make([]string, 0, 1)
			if pending := engine.GetPendingTransaction(); pending != nil {
				if pending.Config.LAN.IPAddress != "" && pending.Config.LAN.IPAddress != active.LAN.IPAddress {
					if ip := net.ParseIP(pending.Config.LAN.IPAddress); ip != nil {
						additionalIPs = append(additionalIPs, ip)
					}
				}
				if pending.Config.WireGuard.Enabled {
					if ip, _, parseErr := net.ParseCIDR(pending.Config.WireGuard.Address); parseErr == nil {
						activeWG, _, _ := net.ParseCIDR(active.WireGuard.Address)
						if activeWG == nil || !ip.Equal(activeWG) {
							additionalIPs = append(additionalIPs, ip)
						}
					}
				}
				candidateName := strings.TrimSuffix(strings.TrimSpace(pending.Config.System.Hostname+"."+pending.Config.System.Domain), ".")
				activeName := strings.TrimSuffix(strings.TrimSpace(active.System.Hostname+"."+active.System.Domain), ".")
				if candidateName != "" && !strings.EqualFold(candidateName, activeName) {
					additionalDNS = append(additionalDNS, candidateName)
				}
			}

			cacheKey := certificateCacheKey(active, additionalIPs, additionalDNS)
			if cachedCert != nil && cachedCertKey == cacheKey &&
				tlsutil.CertificateTimeValid(cachedNotBefore, cachedNotAfter, time.Now()) {
				return cachedCert, nil
			}

			activeCertPEM, activeKeyPEM, certErr := certMgr.EnsureCertificateWithAdditionalSANs(&active, additionalIPs, additionalDNS)
			if certErr != nil {
				return nil, certErr
			}
			activeCert, certErr := tls.X509KeyPair(activeCertPEM, activeKeyPEM)
			if certErr != nil {
				return nil, certErr
			}
			leaf := activeCert.Leaf
			if leaf == nil {
				leaf, certErr = x509.ParseCertificate(activeCert.Certificate[0])
				if certErr != nil {
					return nil, certErr
				}
			}
			cachedCert = &activeCert
			cachedCertKey = cacheKey
			cachedNotBefore = leaf.NotBefore
			cachedNotAfter = leaf.NotAfter
			return cachedCert, nil
		},
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
		bindHost := "127.0.0.1"
		if os.Getenv("MINIMALROUTER_PREVIEW_LAN") == "1" {
			bindHost = "0.0.0.0"
		}
		previewPort := os.Getenv("MINIMALROUTER_PREVIEW_PORT")
		if previewPort == "" {
			previewPort = "8080"
		}
		serverAddr = net.JoinHostPort(bindHost, previewPort)
	}

	srv := &http.Server{
		Addr:              serverAddr,
		Handler:           managementDestinationHandler(engine, storagePressureHandler(absDir, mux)),
		TLSConfig:         tlsConfig,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       15 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	listener, err := net.Listen("tcp", serverAddr)
	if err != nil {
		log.Fatalf("Refusing startup because management listener could not bind: %v", err)
	}
	defer listener.Close()
	if err := signalRouterdReady(initialCfg.Revision); err != nil {
		log.Fatalf("Refusing startup because OpenRC readiness could not be published: %v", err)
	}

	log.Printf("routerd listening on firewall-confined management endpoint https://%s:%d/api/v1/\n", initialCfg.LAN.IPAddress, port)
	log.Printf("Certificate fingerprint displayed above - verify on first connect\n")

	if previewHTTP {
		scope := "loopback-only"
		if os.Getenv("MINIMALROUTER_PREVIEW_LAN") == "1" {
			scope = "LAN-accessible"
		}
		previewPort := os.Getenv("MINIMALROUTER_PREVIEW_PORT")
		if previewPort == "" {
			previewPort = "8080"
		}
		log.Printf("[PREVIEW] Dashboard available on %s http://127.0.0.1:%s", scope, previewPort)
	}

	// OpenRC sends SIGTERM and waits 10s before SIGKILL. Without this the
	// process was always killed hard: every deferred close in this function was
	// unreachable, so SQLite never got a clean close and the gateway store was
	// left to crash recovery on every single reboot.
	shutdownCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopSignals()

	serveErr := make(chan error, 1)
	go func() {
		if previewHTTP {
			serveErr <- srv.Serve(listener)
			return
		}
		serveErr <- srv.ServeTLS(listener, "", "")
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server error: %v", err)
		}
	case <-shutdownCtx.Done():
		stopSignals()
		log.Println("Shutdown signal received; draining management connections")
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 8*time.Second)
		if err := srv.Shutdown(drainCtx); err != nil {
			log.Printf("Graceful shutdown incomplete: %v", err)
			_ = srv.Close()
		}
		drainCancel()
		<-serveErr
		log.Println("Management plane stopped cleanly")
	}
}

// certificateCacheKey captures every input that changes the generated SAN set.
// A miss only costs one regeneration; a stale hit would serve a certificate
// missing a pending LAN or WireGuard address, so every contributing field is
// included verbatim.
func certificateCacheKey(active config.SystemConfig, additionalIPs []net.IP, additionalDNS []string) string {
	var builder strings.Builder
	builder.WriteString(active.LAN.IPAddress)
	builder.WriteByte('|')
	builder.WriteString(active.WireGuard.Address)
	builder.WriteByte('|')
	if active.WireGuard.Enabled {
		builder.WriteString("wg")
	}
	builder.WriteByte('|')
	builder.WriteString(active.System.Hostname)
	builder.WriteByte('.')
	builder.WriteString(active.System.Domain)
	for _, ip := range additionalIPs {
		builder.WriteByte('|')
		builder.WriteString(ip.String())
	}
	for _, name := range additionalDNS {
		builder.WriteByte('|')
		builder.WriteString(name)
	}
	return builder.String()
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
			addConfigAddresses(current, true)
			addConfigAddresses(pending.Config, true)
		}

		if os.Getenv("MINIMALROUTER_PREVIEW_LAN") == "1" {
			// macOS preview: allow the host's own LAN addresses as management
			// destinations so the dashboard is reachable from other devices.
			if addrs, err := net.InterfaceAddrs(); err == nil {
				for _, addr := range addrs {
					if ipnet, ok := addr.(*net.IPNet); ok {
						if ip := ipnet.IP.To4(); ip != nil {
							allowed[ip.String()] = struct{}{}
							allowedHosts[ip.String()] = struct{}{}
						}
					}
				}
			}
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
