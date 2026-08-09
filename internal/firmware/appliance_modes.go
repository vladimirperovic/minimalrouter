package firmware

import (
	"fmt"
	"os"
	"path/filepath"
)

// ValidateApplianceFileModes enforces the executable contract that is not part
// of a SHA-256 content digest. A correctly signed archive with one daemon's
// execute/read permission restricted to root would otherwise make slot-exec
// silently fall back to the bootstrap binary for the unprivileged service,
// creating a mixed-version routerd/applyd pair. Stage strips write/special bits
// but deliberately preserves source read/execute bits, so the extracted payload
// must already be readable and executable by the unprivileged runtime.
func ValidateApplianceFileModes(root string, manifest *FirmwareManifest) error {
	if err := ValidateAppliancePayload(manifest); err != nil {
		return err
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
	return nil
}
