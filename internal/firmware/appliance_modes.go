package firmware

import (
	"fmt"
	"os"
	"path/filepath"
)

// ValidateApplianceFileModes enforces the executable contract that is not part
// of a SHA-256 content digest. A correctly signed archive with one daemon's
// execute bit missing would otherwise make slot-exec silently fall back to the
// bootstrap binary for that daemon, creating a mixed-version routerd/applyd
// pair. Stage copies source modes with write bits stripped, so proving the
// execute bit here is sufficient for the staged slot contract.
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
		if info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("appliance executable bit is missing: %s", relative)
		}
	}
	return nil
}
