package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

const (
	startupVerifiedHashPath = "/run/minimalrouter/routerd-startup-verified.sha256"
	startupConsumedPath     = "/run/minimalrouter/routerd-state/startup-fastpath-consumed"
	startupVerifiedMaxAge   = 60 * time.Second
)

// startupRuntimeVerified proves that the privileged helper has just restored
// and verified the exact canonical configuration routerd loaded from SQLite.
// The root-created hash cannot be forged by routerd. A separate routerd-owned
// O_EXCL marker makes the optimization one-shot even when supervise-daemon
// respawns routerd without re-running the OpenRC start_pre hook.
func startupRuntimeVerified(cfg config.SystemConfig) bool {
	consumed, err := os.OpenFile(startupConsumedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false
	}
	_ = consumed.Close()
	return startupRuntimeVerifiedAt(cfg, startupVerifiedHashPath, time.Now())
}

func startupRuntimeVerifiedAt(cfg config.SystemConfig, path string, now time.Time) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	// The handoff must not be modifiable by the unprivileged management plane.
	// /run/minimalrouter itself is root:routerd 0750; this additionally rejects
	// an accidentally group/other-writable marker.
	if info.Mode().Perm()&0022 != 0 {
		return false
	}
	age := now.Sub(info.ModTime())
	if age < -5*time.Second || age > startupVerifiedMaxAge {
		return false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	hint := strings.TrimSpace(string(data))
	if len(hint) != sha256.Size*2 {
		return false
	}
	hintBytes, err := hex.DecodeString(hint)
	if err != nil || len(hintBytes) != sha256.Size {
		return false
	}

	canonical, err := json.Marshal(cfg)
	if err != nil {
		return false
	}
	expected := sha256.Sum256(canonical)
	return subtle.ConstantTimeCompare(hintBytes, expected[:]) == 1
}
