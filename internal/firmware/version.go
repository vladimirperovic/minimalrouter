package firmware

import (
	"errors"
	"strings"
)

type parsedReleaseVersion struct {
	core       [3]string
	prerelease []string
}

func parseReleaseVersion(value string) (parsedReleaseVersion, error) {
	if !releaseVersionPattern.MatchString(value) {
		return parsedReleaseVersion{}, errors.New("invalid release version")
	}
	value = strings.TrimPrefix(value, "v")
	if plus := strings.IndexByte(value, '+'); plus >= 0 {
		value = value[:plus]
	}
	pre := ""
	if dash := strings.IndexByte(value, '-'); dash >= 0 {
		pre = value[dash+1:]
		value = value[:dash]
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return parsedReleaseVersion{}, errors.New("invalid release version")
	}
	parsed := parsedReleaseVersion{core: [3]string{parts[0], parts[1], parts[2]}}
	if pre != "" {
		parsed.prerelease = strings.Split(pre, ".")
	}
	return parsed, nil
}

func numericIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func compareNumericStrings(left, right string) int {
	left = strings.TrimLeft(left, "0")
	right = strings.TrimLeft(right, "0")
	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

// compareReleaseVersions implements the SemVer precedence rules needed by the
// updater without adding a dependency to the appliance. Build metadata is
// deliberately ignored. A stable version has higher precedence than a
// prerelease with the same core version.
func compareReleaseVersions(left, right string) (int, error) {
	a, err := parseReleaseVersion(left)
	if err != nil {
		return 0, err
	}
	b, err := parseReleaseVersion(right)
	if err != nil {
		return 0, err
	}
	for i := range a.core {
		if cmp := compareNumericStrings(a.core[i], b.core[i]); cmp != 0 {
			return cmp, nil
		}
	}

	if len(a.prerelease) == 0 && len(b.prerelease) == 0 {
		return 0, nil
	}
	if len(a.prerelease) == 0 {
		return 1, nil
	}
	if len(b.prerelease) == 0 {
		return -1, nil
	}

	limit := len(a.prerelease)
	if len(b.prerelease) < limit {
		limit = len(b.prerelease)
	}
	for i := 0; i < limit; i++ {
		leftID, rightID := a.prerelease[i], b.prerelease[i]
		leftNumeric, rightNumeric := numericIdentifier(leftID), numericIdentifier(rightID)
		switch {
		case leftNumeric && rightNumeric:
			if cmp := compareNumericStrings(leftID, rightID); cmp != 0 {
				return cmp, nil
			}
		case leftNumeric:
			return -1, nil
		case rightNumeric:
			return 1, nil
		default:
			if leftID < rightID {
				return -1, nil
			}
			if leftID > rightID {
				return 1, nil
			}
		}
	}
	if len(a.prerelease) < len(b.prerelease) {
		return -1, nil
	}
	if len(a.prerelease) > len(b.prerelease) {
		return 1, nil
	}
	return 0, nil
}

func validateForwardUpgrade(candidate, current string) error {
	if current == "" {
		return nil
	}
	cmp, err := compareReleaseVersions(candidate, current)
	if err != nil {
		return err
	}
	if cmp <= 0 {
		return errors.New("refusing non-forward update; use the explicit rollback command to return to the previous release")
	}
	return nil
}
