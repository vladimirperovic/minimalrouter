package firmware

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateApplianceFileModes enforces runtime access that a SHA-256 content
// digest cannot establish. Both daemons and all served assets must be usable
// by their real runtime accounts before staging normalizes role-based modes.
func ValidateApplianceFileModes(root string, manifest *FirmwareManifest) error {
	if err := ValidateAppliancePayload(manifest); err != nil {
		return err
	}
	for relative := range manifest.Files {
		info, err := os.Lstat(filepath.Join(root, relative))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("appliance executable is missing or unsafe: %s", relative)
		}
		required := ApplianceFileMode(relative) & 0o555
		if info.Mode().Perm()&required != required || info.Mode().Perm()&0o022 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("appliance file has unsafe runtime permissions: %s", relative)
		}
	}
	return nil
}

// ApplianceFileMode is the installed role contract, independent of archive
// ownership and the operator's umask. The updater remains root-only; recovery
// must also execute as its unprivileged database worker. Its CLI checks root.
func ApplianceFileMode(path string) os.FileMode {
	for _, arch := range supportedArchitectures {
		for _, role := range applianceFileRoles {
			if path == strings.ReplaceAll(role.Path, "{arch}", arch) {
				return role.Mode
			}
		}
	}
	if filepath.Dir(path) == "bin" || strings.HasPrefix(path, "init.d/") {
		return 0o755
	}
	return 0o644
}
