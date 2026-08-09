package firmware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	supportedBootstrapABI    = 1
	supportedConfigSchema    = 1
	supportedRuntimeProtocol = 1
	maxCompatibilityBytes    = 4096
)

type applianceCompatibility struct {
	BootstrapABI    int `json:"bootstrap_abi"`
	ConfigSchema    int `json:"config_schema"`
	RuntimeProtocol int `json:"runtime_protocol"`
}

// ValidateApplianceCompatibility enforces the contract between an activated
// A/B slot and the stable bootstrap/install layer. The metadata is part of the
// signed manifest. VerifyFirmware first verifies the manifest signature, then
// applies this runtime contract and the content hashes. SlotManager.Stage runs
// the same verification again on the copied, root-owned temporary slot before
// it can become pending.
//
// Generic signed payloads that do not carry compatibility.json are left alone;
// complete Minimal Router appliance payloads require the file through
// ValidateAppliancePayload.
func ValidateApplianceCompatibility(root string, manifest *FirmwareManifest) error {
	if manifest == nil {
		return fmt.Errorf("missing firmware manifest")
	}
	if _, ok := manifest.Files["compatibility.json"]; !ok {
		return nil
	}

	path := filepath.Join(root, "compatibility.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("compatibility metadata is missing or unsafe")
	}
	if info.Size() <= 0 || info.Size() > maxCompatibilityBytes {
		return fmt.Errorf("compatibility metadata size is invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read compatibility metadata: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var compatibility applianceCompatibility
	if err := decoder.Decode(&compatibility); err != nil {
		return fmt.Errorf("decode compatibility metadata: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("compatibility metadata must contain exactly one JSON object")
	}
	if compatibility.BootstrapABI != supportedBootstrapABI {
		return fmt.Errorf("unsupported bootstrap ABI %d", compatibility.BootstrapABI)
	}
	if compatibility.ConfigSchema != supportedConfigSchema {
		return fmt.Errorf("unsupported config schema %d", compatibility.ConfigSchema)
	}
	if compatibility.RuntimeProtocol != supportedRuntimeProtocol {
		return fmt.Errorf("unsupported runtime protocol %d", compatibility.RuntimeProtocol)
	}
	return nil
}
