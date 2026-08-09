package firmware

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateApplianceArchitecture binds a complete appliance payload to the
// architecture of the machine that will activate it. ValidateAppliancePayload
// already requires exactly one complete architecture binary set; this check
// prevents a correctly signed ARM64 release from being staged on AMD64 (or the
// reverse), which would otherwise make slot-exec fall back to bootstrap daemons
// while still exposing the newly activated web tree.
func ValidateApplianceArchitecture(manifest *FirmwareManifest, goarch string) error {
	if err := ValidateAppliancePayload(manifest); err != nil {
		return err
	}
	var binary string
	switch goarch {
	case "amd64":
		binary = "bin/routerd-amd64"
	case "arm64":
		binary = "bin/routerd-arm64"
	default:
		return fmt.Errorf("unsupported Minimal Router architecture: %s", goarch)
	}
	if _, ok := manifest.Files[binary]; !ok {
		return fmt.Errorf("appliance payload architecture does not match running %s system", goarch)
	}
	return nil
}

// ValidateApplianceFileModes enforces the complete appliance payload contract
// plus runtime contracts that are not represented by file-content hashes. A
// correctly signed archive with one daemon restricted to root could make
// slot-exec fall back to a bootstrap binary; a root-only web asset could
// similarly make routerd fall back to the bootstrap frontend or serve a
// partially unreadable dashboard.
func ValidateApplianceFileModes(root string, manifest *FirmwareManifest) error {
	if err := ValidateAppliancePayload(manifest); err != nil {
		return err
	}
	return ValidateManifestRuntimeFileModes(root, manifest)
}

// ValidateManifestRuntimeFileModes enforces the permission and stable-bootstrap
// compatibility invariants required by files consumed from an active A/B slot.
// It intentionally does not require a complete appliance payload so
// SlotManager/VerifyFirmware and focused tests can use the same verifier.
//
// Firmware signatures cover file contents but not Unix mode metadata. This
// validation therefore runs on both the extracted source and the copied slot: a
// writable staging directory must not be able to change a file from 0755/0644
// to 0700/0600 between an outer preflight and the final copy. If a signed
// compatibility.json is present, its bootstrap/schema/protocol contract is also
// checked on both passes; complete appliance payloads require that file.
func ValidateManifestRuntimeFileModes(root string, manifest *FirmwareManifest) error {
	if manifest == nil {
		return fmt.Errorf("missing firmware manifest")
	}

	requiredExecutable := map[string]struct{}{
		"slot-exec":                 {},
		"install.sh":                {},
		"init.d/routerd":            {},
		"init.d/router-applyd":      {},
		"init.d/pppoe-wan":          {},
		"ip-up.d-minimalrouter-qos": {},
	}

	for relative := range manifest.Files {
		clean := filepath.Clean(relative)
		if clean == "." || filepath.IsAbs(clean) || clean != relative ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe appliance path: %q", relative)
		}

		_, fixedExecutable := requiredExecutable[relative]
		isExecutable := fixedExecutable || filepath.Dir(relative) == "bin"
		isWebAsset := strings.HasPrefix(relative, "web/dist/")
		if !isExecutable && !isWebAsset {
			continue
		}

		info, err := os.Lstat(filepath.Join(root, relative))
		if err != nil || !info.Mode().IsRegular() {
			if isExecutable {
				return fmt.Errorf("appliance executable is missing or unsafe: %s", relative)
			}
			return fmt.Errorf("appliance web asset is missing or unsafe: %s", relative)
		}

		if isExecutable && info.Mode().Perm()&0o555 != 0o555 {
			return fmt.Errorf("appliance executable is not readable/executable by unprivileged services: %s", relative)
		}
		if isWebAsset && info.Mode().Perm()&0o444 != 0o444 {
			return fmt.Errorf("appliance web asset is not readable by unprivileged services: %s", relative)
		}
	}
	return ValidateApplianceCompatibility(root, manifest)
}
