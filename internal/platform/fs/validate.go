package fs

import (
	"path"
	"strings"

	"design-cloud-engine/internal/contract"
)

// NormalizeTrashPath sanitizes a trash-relative path by:
//   - allowing empty string (means the trash root)
//   - rejecting absolute or escaping paths (..)
//   - returning the cleaned relative path on success
func NormalizeTrashPath(trashPath string) (string, error) {
	if trashPath == "" {
		return "", nil
	}
	if path.IsAbs(trashPath) {
		return "", contract.NewValidationError("invalid trash path")
	}
	cleanPath := path.Clean(trashPath)
	if cleanPath == "." {
		return "", nil
	}
	if cleanPath == ".." || strings.HasPrefix(cleanPath, "../") {
		return "", contract.NewValidationError("invalid trash path")
	}
	return cleanPath, nil
}

// NormalizeTrashName sanitizes a single trash item name by:
//   - rejecting empty, absolute, or escaping names
//   - rejecting names containing path separators
//   - returning the cleaned name on success
func NormalizeTrashName(trashName string) (string, error) {
	if trashName == "" {
		return "", contract.NewValidationError("invalid trash name")
	}
	if path.IsAbs(trashName) {
		return "", contract.NewValidationError("invalid trash name")
	}
	cleanName := path.Clean(trashName)
	if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, "../") || strings.Contains(cleanName, "/") {
		return "", contract.NewValidationError("invalid trash name")
	}
	return cleanName, nil
}
