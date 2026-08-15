package firmware

// CompareReleaseVersions exposes the updater's SemVer precedence rules to the
// management plane without duplicating version parsing outside this package.
func CompareReleaseVersions(left, right string) (int, error) {
	return compareReleaseVersions(left, right)
}

// IsReleaseVersion reports whether value is a syntactically valid release tag.
func IsReleaseVersion(value string) bool {
	return releaseVersionPattern.MatchString(value)
}
