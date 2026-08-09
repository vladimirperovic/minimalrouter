package firmware

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateApplianceFileModes enforces runtime permission contracts that are not
// part of a SHA-256 content digest. A correctly signed archive with one daemon
// restricted to root could make slot-exec fall back to a bootstrap binary; a
// root-only web asset could similarly make routerd fall back to the bootstrap
// frontend or serve a partially unreadable dashboard. Stage strips write and
// special bits but deliberately preserves source read/execute bits, so the
// extracted payload must already be usable by the unprivileged runtime.
func ValidateApplianceFileModes(root string, manifest *FirmwareManifest) error {
	if err := ValidateAppliancePayload(manifest); err != nil {
		return err
	}

	// This validation runs before SlotManager.Stage performs the full signed
	// hash verification. Reject unsafe manifest paths before using any of them
	// for mode inspection so mode preflight itself cannot escape the payload
	// root through a crafted relative path.
	for relative := range manifest.Files {
		clean := filepath.Clean(relative)
		if clean == "." || filepath.IsAbs(clean) || clean != relative ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe appliance path: %q", relative)
		}
	}

	requiredExecutable := []string{
		"slot-exec",
		"install.sh",
		"init.d/routerd",
		"init.d/router-applyd",
		"init.d/pppoe-wan",
		"ip-up.d-minimalrouter-qos",
	}
	for path := range manifest.Files {
		if filepath.Dir(path) == "bin" {
			requiredExecutable = append(requiredExecutable, path)
		}
	}
	for _, relative := range requiredExecutable {
		info, err := os.Lstat(filepath.Join(root, relative))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("appliance executable is missing or unsafe: %s", relative)
		}
		// Firmware signatures cover content hashes, not archive mode metadata.
		// Requiring r-x for owner/group/other prevents a tampered or
		// restrictively-extracted 0700/0750 payload from recreating the exact
		// mixed-runtime failure seen in the Proxmox lab. Stage subsequently
		// removes write/special bits, so 0555 is the minimum runtime contract.
		if info.Mode().Perm()&0o555 != 0o555 {
			return fmt.Errorf("appliance executable is not readable/executable by unprivileged services: %s", relative)
		}
	}

	for relative := range manifest.Files {
		if !strings.HasPrefix(relative, "web/dist/") {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, relative))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("appliance web asset is missing or unsafe: %s", relative)
		}
		// Active slots are root-owned while routerd is unprivileged. Requiring
		// world-readable web content prevents a signed 0600/0640 archive mode
		// from forcing bootstrap-frontend fallback or breaking nested assets.
		if info.Mode().Perm()&0o444 != 0o444 {
			return fmt.Errorf("appliance web asset is not readable by unprivileged services: %s", relative)
		}
	}
	return nil
}
