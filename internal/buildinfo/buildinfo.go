package buildinfo

import "strings"

// These values are intentionally variables so release builds can inject them
// with -ldflags -X. Development builds keep safe, explicit fallbacks instead of
// pretending to be a published release.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// DisplayVersion returns a stable human-facing version string. Release builds
// normally inject a leading-v tag (for example v0.1.3), but accepting a bare
// semantic version keeps local packaging and downstream builds predictable.
func DisplayVersion() string {
	version := strings.TrimSpace(Version)
	if version == "" || version == "dev" {
		return "dev"
	}
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}
