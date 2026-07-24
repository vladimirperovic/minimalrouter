package telemetry

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

var (
	passwordRegex = regexp.MustCompile(`(?i)("?password"?\s*[:=]\s*)"[^"]+"`)
	tokenRegex    = regexp.MustCompile(`(?i)("?token"?\s*[:=]\s*)"[^"]+"`)
	keyRegex      = regexp.MustCompile(`(?i)("?private_key"?\s*[:=]\s*)"[^"]+"`)
	secretRegex   = regexp.MustCompile(`(?i)("?secret"?\s*[:=]\s*)"[^"]+"`)
)

// RedactSecrets redacts sensitive credentials from logs and export payloads per SECURITY.md §9.
func RedactSecrets(input string) string {
	out := passwordRegex.ReplaceAllString(input, `$1"[REDACTED]"`)
	out = tokenRegex.ReplaceAllString(out, `$1"[REDACTED]"`)
	out = keyRegex.ReplaceAllString(out, `$1"[REDACTED]"`)
	out = secretRegex.ReplaceAllString(out, `$1"[REDACTED]"`)
	return out
}

// RedactedSystemConfig returns a copy of SystemConfig with secret fields scrubbed for telemetry/export.
func RedactedSystemConfig(cfg config.SystemConfig) config.SystemConfig {
	clean := cfg
	if clean.WAN.Password != "" {
		clean.WAN.Password = "[REDACTED]"
	}
	return clean
}

// DiagnosticBundle represents a sanitized export of system state for troubleshooting.
type DiagnosticBundle struct {
	Timestamp      time.Time           `json:"timestamp"`
	AppVersion     string              `json:"app_version"`
	RedactedConfig config.SystemConfig `json:"config"`
	ServiceHealth  map[string]string   `json:"service_health"`
}

// BuildDiagnosticBundle constructs a safe, redacted diagnostic report.
func BuildDiagnosticBundle(cfg config.SystemConfig) ([]byte, error) {
	bundle := DiagnosticBundle{
		Timestamp:      time.Now(),
		AppVersion:     "v0.1-alpha",
		RedactedConfig: RedactedSystemConfig(cfg),
		ServiceHealth: map[string]string{
			"pppd":        "running",
			"nftables":    "active",
			"dnsmasq":     "running",
			"wireguard":   "disabled",
			"cloudflared": "disabled",
		},
	}

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, err
	}

	return []byte(RedactSecrets(string(data))), nil
}
