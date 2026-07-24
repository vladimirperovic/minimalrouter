package telemetry

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestRedactSecrets(t *testing.T) {
	raw := `{"username":"user1","password":"mySecretPassword123","token":"ghp_abc123"}`
	redacted := RedactSecrets(raw)

	if strings.Contains(redacted, "mySecretPassword123") {
		t.Errorf("Expected password to be redacted from log string")
	}
	if !strings.Contains(redacted, "[REDACTED]") {
		t.Errorf("Expected [REDACTED] placeholder in text")
	}
}

func TestBuildDiagnosticBundle(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Password = "SuperSecretPass!"

	bundleBytes, err := BuildDiagnosticBundle(cfg)
	if err != nil {
		t.Fatalf("BuildDiagnosticBundle failed: %v", err)
	}

	bundleStr := string(bundleBytes)
	if strings.Contains(bundleStr, "SuperSecretPass!") {
		t.Errorf("Expected WAN password to be redacted from diagnostic bundle")
	}
}
