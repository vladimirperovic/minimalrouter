package firmware

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateApplianceCompatibility(t *testing.T) {
	write := func(t *testing.T, content string) (string, *FirmwareManifest) {
		t.Helper()
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "compatibility.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return root, &FirmwareManifest{Files: map[string]string{"compatibility.json": "fixture-hash"}}
	}

	root, manifest := write(t, "{\"bootstrap_abi\":1,\"config_schema\":1,\"runtime_protocol\":1}\n")
	if err := ValidateApplianceCompatibility(root, manifest); err != nil {
		t.Fatalf("supported compatibility metadata rejected: %v", err)
	}

	for name, content := range map[string]string{
		"bootstrap ABI":    "{\"bootstrap_abi\":2,\"config_schema\":1,\"runtime_protocol\":1}",
		"config schema":    "{\"bootstrap_abi\":1,\"config_schema\":2,\"runtime_protocol\":1}",
		"runtime protocol": "{\"bootstrap_abi\":1,\"config_schema\":1,\"runtime_protocol\":2}",
		"unknown field":    "{\"bootstrap_abi\":1,\"config_schema\":1,\"runtime_protocol\":1,\"extra\":true}",
		"trailing object":  "{\"bootstrap_abi\":1,\"config_schema\":1,\"runtime_protocol\":1}{}",
	} {
		t.Run(name, func(t *testing.T) {
			root, manifest := write(t, content)
			if err := ValidateApplianceCompatibility(root, manifest); err == nil {
				t.Fatalf("unsupported compatibility metadata accepted: %s", content)
			}
		})
	}
}

func TestValidateApplianceCompatibilityIgnoresGenericPayload(t *testing.T) {
	manifest := &FirmwareManifest{Files: map[string]string{"payload": "fixture-hash"}}
	if err := ValidateApplianceCompatibility(t.TempDir(), manifest); err != nil {
		t.Fatalf("generic signed payload was forced into appliance compatibility: %v", err)
	}
}
