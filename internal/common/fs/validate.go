package fs

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
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
		return "", fmt.Errorf("%w: invalid trash path", fs.ErrInvalid)
	}
	cleanPath := path.Clean(trashPath)
	if cleanPath == "." {
		return "", nil
	}
	if cleanPath == ".." || strings.HasPrefix(cleanPath, "../") {
		return "", fmt.Errorf("%w: invalid trash path", fs.ErrInvalid)
	}
	return cleanPath, nil
}

// NormalizeTrashName sanitizes a single trash item name by:
//   - rejecting empty, absolute, or escaping names
//   - rejecting names containing path separators
//   - returning the cleaned name on success
func NormalizeTrashName(trashName string) (string, error) {
	if trashName == "" {
		return "", fmt.Errorf("%w: invalid trash name", fs.ErrInvalid)
	}
	if path.IsAbs(trashName) {
		return "", fmt.Errorf("%w: invalid trash name", fs.ErrInvalid)
	}
	cleanName := path.Clean(trashName)
	if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, "../") || strings.Contains(cleanName, "/") {
		return "", fmt.Errorf("%w: invalid trash name", fs.ErrInvalid)
	}
	return cleanName, nil
}
