package firmware

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"runtime"
)

// ValidateReleaseCandidate is the single pre-stage validation boundary for a
// signed Minimal Router appliance. Verify signed paths and file contents first,
// then enforce the executable/layout and host-architecture contracts that are
// not represented by content hashes alone. API preflight and SlotManager.Stage
// use this same function so "valid" means "acceptable to stage on this host",
// not merely "signature matches".
func ValidateReleaseCandidate(root string, manifest *FirmwareManifest, trustedKey ed25519.PublicKey) error {
	return ValidateReleaseCandidateForArch(root, manifest, trustedKey, runtime.GOARCH)
}

// ValidateReleaseCandidateForArch is exposed separately so architecture policy
// can be regression-tested without depending on the architecture running the
// test suite.
func ValidateReleaseCandidateForArch(root string, manifest *FirmwareManifest, trustedKey ed25519.PublicKey, arch string) error {
	if err := VerifyFirmware(root, manifest, trustedKey); err != nil {
		return fmt.Errorf("verify signed release: %w", err)
	}
	if err := ValidateApplianceFileModes(root, manifest); err != nil {
		return fmt.Errorf("validate appliance layout: %w", err)
	}
	if err := ValidateApplianceArchitecture(manifest, arch); err != nil {
		return fmt.Errorf("validate appliance architecture: %w", err)
	}
	return nil
}

// ValidateApplianceArchitecture proves that the one complete binary set in the
// signed payload matches the running appliance. Activation already has an exact
// architecture-specific bootstrap check; rejecting earlier keeps API preflight
// and staging from reporting a package usable when it can never activate.
func ValidateApplianceArchitecture(manifest *FirmwareManifest, arch string) error {
	if manifest == nil {
		return errors.New("missing appliance manifest")
	}
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("unsupported update architecture %q", arch)
	}
	for _, name := range []string{"routerd", "router-applyd", "router-recovery", "router-update"} {
		path := "bin/" + name + "-" + arch
		if _, ok := manifest.Files[path]; !ok {
			return fmt.Errorf("signed appliance is not built for %s", arch)
		}
	}
	return nil
}
