package file

import (
	"errors"
	"time"

	"design-cloud-engine/internal/execution/payload"
)

var (
	ErrUploadTooLarge     = errors.New("file too large")
	ErrNoUploadPermission = errors.New("no upload permission")

	ErrInvalidFilePath            = errors.New("invalid file path")
	ErrNotAllowDownload           = errors.New("not allow to download this file")
	ErrDownloadTooLarge           = errors.New("download file too large")
	ErrDownloadFileChanged        = errors.New("download file changed after approval")
	ErrDownloadRequestExpired     = errors.New("download request expired")
	ErrInvalidDownloadToken       = errors.New("invalid or expired download token")
	ErrNoDownloadPermission       = errors.New("you don't have permission to download this file")
	ErrDownloadRequestNotFound    = errors.New("download request not found")
	ErrDownloadRequestNotApproved = errors.New("download request not approved")
	ErrDownloadRequestRejected    = errors.New("download request rejected")
	ErrInvalidDownloadStatus      = errors.New("invalid download request status")
	ErrDownloadApprovalRequired   = errors.New("download approval is required")
	ErrDownloadApprovalNotNeeded  = errors.New("download approval is not required")
	ErrDownloadRequestConflict    = errors.New("download request is not pending")
)

// PredefinedDir is a symbolic name for a built-in directory location.
type PredefinedDir string

const (
	PredefinedDirHome     PredefinedDir = "home"
	PredefinedDirReleases PredefinedDir = "releases"
)

// DownloadPermission controls whether a user is allowed to download files.
type DownloadPermission string

const (
	DownloadPermissionAllow   DownloadPermission = "allow"
	DownloadPermissionDeny    DownloadPermission = "deny"
	DownloadPermissionRequest DownloadPermission = "request"
)

// DownloadToken is an opaque handle returned to the client that grants a
// one-shot download within its TTL window.
type DownloadToken struct {
	Token     string
	ExpiresAt time.Time
}

// LocationAccessPolicy describes whether a location exists and is writable.
type LocationAccessPolicy struct {
	Exists   bool
	ReadOnly bool
}

// DirInfo is the listing result returned by ListDir.
type DirInfo struct {
	Name  string             `json:"name"`
	Path  string             `json:"path"`
	Files []payload.FileInfo `json:"files"`
}

// LocationInfo is the metadata of a configured location.
type LocationInfo struct {
	Name     string `json:"name"`
	BuiltIn  bool   `json:"builtIn"`
	ReadOnly bool   `json:"readOnly"`
}

// TrashDirInfo is the listing result of a user's trash directory.
type TrashDirInfo struct {
	TrashPath string           `json:"trashPath"`
	Files     []trashFileEntry `json:"files"`
}
