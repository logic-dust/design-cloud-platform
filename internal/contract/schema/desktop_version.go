package schema

import (
	"design-cloud-engine/internal/contract"
	"regexp"
	"unicode/utf8"
)

const maxMinSupportedVersionLen = 32

// minSupportedVersionPattern is a strict core semver: MAJOR.MINOR.PATCH
// with no leading zeros, prefix, pre-release, or build metadata.
var minSupportedVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ValidateMinSupportedVersion accepts an empty value (no minimum) or a
// strict MAJOR.MINOR.PATCH string at most 32 characters.
func ValidateMinSupportedVersion(v string) error {
	if v == "" {
		return nil
	}
	if utf8.RuneCountInString(v) > maxMinSupportedVersionLen || !minSupportedVersionPattern.MatchString(v) {
		return contract.NewValidationError("minSupportedVersion must be MAJOR.MINOR.PATCH")
	}
	return nil
}
