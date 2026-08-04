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

// RedactedSystemConfig returns a detached copy of SystemConfig with secret
// fields scrubbed for telemetry/export. The copy is deep so the redaction can
// never mutate the canonical configuration in memory.
func RedactedSystemConfig(cfg config.SystemConfig) config.SystemConfig {
	clean := cfg.DeepCopy()
	if clean.WAN.Password != "" {
		clean.WAN.Password = "[REDACTED]"
	}
	if clean.SquidProxy.Password != "" {
		clean.SquidProxy.Password = "[REDACTED]"
	}
	if clean.WiFi.Passphrase != "" {
		clean.WiFi.Passphrase = "[REDACTED]"
	}
	if clean.WiFi.SSID != "" {
		clean.WiFi.SSID = ""
	}
	if clean.WireGuard.PrivateKey != "" {
		clean.WireGuard.PrivateKey = "[REDACTED]"
	}
	if clean.WGClient.PrivateKey != "" {
		clean.WGClient.PrivateKey = "[REDACTED]"
	}
	if clean.WGClient.PresharedKey != "" {
		clean.WGClient.PresharedKey = "[REDACTED]"
	}
	for i := range clean.WireGuard.Peers {
		clean.WireGuard.Peers[i].Name = ""
		if clean.WireGuard.Peers[i].PresharedKey != "" {
			clean.WireGuard.Peers[i].PresharedKey = "[REDACTED]"
		}
	}
	for i := range clean.AdGuard.DeviceProfiles {
		clean.AdGuard.DeviceProfiles[i].Name = ""
	}
	for i := range clean.DHCP.StaticLeases {
		clean.DHCP.StaticLeases[i].Hostname = ""
		clean.DHCP.StaticLeases[i].MAC = ""
	}
	for i := range clean.DNS.Records {
		clean.DNS.Records[i].Name = ""
	}
	for i := range clean.Firewall.ExtraLANs {
		clean.Firewall.ExtraLANs[i].Name = ""
	}
	if clean.Cloudflare.APIToken != "" {
		clean.Cloudflare.APIToken = "[REDACTED]"
	}
	if clean.Cloudflare.TunnelToken != "" {
		clean.Cloudflare.TunnelToken = "[REDACTED]"
	}
	// The DDNS hostname identifies the operator's public domain and is not
	// needed in a diagnostic export.
	if clean.Cloudflare.Domain != "" {
		clean.Cloudflare.Domain = ""
	}
	if clean.Cloudflare.DDNSUser != "" {
		clean.Cloudflare.DDNSUser = ""
	}
	return clean
}

// DiagnosticBundle represents a sanitized export of system state for
// troubleshooting. The bundle still contains private topology details (CIDRs,
// static IPs), so it must be treated as PRIVATE, never shared as a public
// issue attachment.
type DiagnosticBundle struct {
	Timestamp      time.Time           `json:"timestamp"`
	AppVersion     string              `json:"app_version"`
	Privacy        string              `json:"privacy"`
	RedactedConfig config.SystemConfig `json:"config"`
	ServiceHealth  map[string]string   `json:"service_health"`
}

// BuildDiagnosticBundle constructs a safe, redacted diagnostic report.
func BuildDiagnosticBundle(cfg config.SystemConfig) ([]byte, error) {
	bundle := DiagnosticBundle{
		Timestamp:      time.Now(),
		AppVersion:     "v0.1-alpha",
		Privacy:        "PRIVATE — contains network topology; do not share publicly",
		RedactedConfig: RedactedSystemConfig(cfg),
		ServiceHealth: map[string]string{
			"pppd":        "not-collected",
			"nftables":    "not-collected",
			"dnsmasq":     "not-collected",
			"wireguard":   "not-implemented",
			"cloudflared": "not-implemented",
		},
	}

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, err
	}

	return []byte(RedactSecrets(string(data))), nil
}
