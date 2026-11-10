package file

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/model"
)

const (
	maxDownloadSizeBytes int64 = 100 * 1024 * 1024 * 1024
	downloadListLimit          = 500
)

// CheckDownloadPermission returns the effective download permission for the given user,
// enforcing both the global policy and the per-user DownloadAccess override.
func (s *Service) CheckDownloadPermission(ctx context.Context, requestUser string) DownloadPermission {
	policy := s.downloadPolicy(ctx)

	// Global deny: no need to check user record.
	if policy == DownloadPermissionDeny {
		return DownloadPermissionDeny
	}

	// For allow and request policies, always check the per-user override so that
	// administrators can restrict individual users regardless of the global setting.
	userRecord, err := s.userSvc.GetDBUser(ctx, requestUser)
	if err != nil {
		s.logger.Warn("failed to load user for download permission", "user", requestUser, "error", err)
		return DownloadPermissionDeny
	}
	if !userRecord.DownloadAccess {
		return DownloadPermissionDeny
	}

	return policy
}

// ResolveDownloadPath validates download inputs and resolves the location-scoped path.
func (s *Service) ResolveDownloadPath(ctx context.Context, locationName, relPath, requestUser string) (string, error) {
	if locationName == "" {
		return "", contract.NewInvalidParamError("location is required")
	}
	cleanRel, err := normalizeLocationPath(relPath)
	if err != nil {
		return "", err
	}
	if cleanRel == "" {
		return "", contract.NewInvalidParamError("path is required")
	}
	return s.ResolveLocationPath(ctx, locationName, cleanRel, requestUser)
}

// OpenAllowedLocationDownload validates permission and returns the resolved file path and size.
func (s *Service) OpenAllowedLocationDownload(ctx context.Context, locationName, relPath, requestUser string) (string, int64, error) {
	filePath, err := s.ResolveDownloadPath(ctx, locationName, relPath, requestUser)
	if err != nil {
		return "", 0, err
	}
	return s.openAllowedDownloadFile(ctx, filePath, requestUser)
}

func (s *Service) openAllowedDownloadFile(ctx context.Context, filePath, requestUser string) (string, int64, error) {
	permission := s.CheckDownloadPermission(ctx, requestUser)
	if permission == DownloadPermissionDeny {
		return "", 0, ErrNoDownloadPermission
	}
	if permission == DownloadPermissionRequest {
		return "", 0, ErrDownloadApprovalRequired
	}

	snapshot, err := s.downloadFileSnapshot(ctx, filePath, requestUser)
	if err != nil {
		return "", 0, err
	}
	if err := s.ensureDownloadSnapshotWithinAllowedRoots(ctx, &snapshot, requestUser); err != nil {
		return "", 0, err
	}
	return filePath, snapshot.Size, nil
}

// OpenApprovedDownloadRequest validates an approved download request and returns file path and size.
func (s *Service) OpenApprovedDownloadRequest(ctx context.Context, id int, requestUser string) (string, int64, error) {
	if s.CheckDownloadPermission(ctx, requestUser) == DownloadPermissionDeny {
		return "", 0, ErrNoDownloadPermission
	}

	downloadInfo, err := s.GetDownloadRequest(ctx, id, requestUser)
	if err != nil {
		return "", 0, err
	}
	if downloadInfo.Status == model.DownloadRequestStatusRejected {
		return "", 0, ErrDownloadRequestRejected
	}
	if downloadInfo.Status != model.DownloadRequestStatusApproved {
		return "", 0, ErrDownloadRequestNotApproved
	}
	if downloadInfo.ExpiresAt != nil && time.Now().After(*downloadInfo.ExpiresAt) {
		return "", 0, ErrDownloadRequestExpired
	}
	snapshot, err := s.downloadFileSnapshot(ctx, downloadInfo.Path, requestUser)
	if err != nil {
		return "", 0, err
	}
	if err := s.ensureDownloadSnapshotWithinAllowedRoots(ctx, &snapshot, requestUser); err != nil {
		return "", 0, err
	}
	if downloadInfo.FileFingerprint == "" || downloadInfo.FileFingerprint != fileSnapshotFingerprint(&snapshot) {
		return "", 0, ErrDownloadFileChanged
	}
	return downloadInfo.Path, snapshot.Size, nil
}

// SubmitDownloadRequest creates a pending download request and pins the current file fingerprint.
func (s *Service) SubmitDownloadRequest(ctx context.Context, filePath, requestUser string) (model.DownloadRequest, error) {
	permission := s.CheckDownloadPermission(ctx, requestUser)
	if permission == DownloadPermissionDeny {
		return model.DownloadRequest{}, ErrNoDownloadPermission
	}
	if permission == DownloadPermissionAllow {
		return model.DownloadRequest{}, ErrDownloadApprovalNotNeeded
	}

	snapshot, err := s.downloadFileSnapshot(ctx, filePath, requestUser)
	if err != nil {
		return model.DownloadRequest{}, err
	}
	if err := s.ensureDownloadSnapshotWithinAllowedRoots(ctx, &snapshot, requestUser); err != nil {
		return model.DownloadRequest{}, err
	}

	// Approval records pin the current file identity without reading the whole file.
	download := model.DownloadRequest{
		Path:            filePath,
		FileFingerprint: fileSnapshotFingerprint(&snapshot),
		Size:            snapshot.Size,
		Requester:       requestUser,
		Status:          model.DownloadRequestStatusPending,
	}
	if err := s.downloadRepo.Create(ctx, &download); err != nil {
		return model.DownloadRequest{}, err
	}
	return download, nil
}

func (s *Service) SubmitLocationDownloadRequest(ctx context.Context, locationName, relPath, requestUser string) (model.DownloadRequest, error) {
	filePath, err := s.ResolveDownloadPath(ctx, locationName, relPath, requestUser)
	if err != nil {
		return model.DownloadRequest{}, err
	}
	return s.SubmitDownloadRequest(ctx, filePath, requestUser)
}

func (s *Service) ListDownloadRequests(ctx context.Context, status model.DownloadRequestStatus, requester string) ([]model.DownloadRequest, error) {
	if status != 0 && !validDownloadStatus(status) {
		return nil, ErrInvalidDownloadStatus
	}
	return s.downloadRepo.List(ctx, status, requester, downloadListLimit)
}

func (s *Service) GetDownloadRequest(ctx context.Context, id int, requester string) (model.DownloadRequest, error) {
	downloadInfo, err := s.downloadRepo.GetByID(ctx, uint(id))
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return model.DownloadRequest{}, ErrDownloadRequestNotFound
		}
		return model.DownloadRequest{}, fmt.Errorf("load download request: %w", err)
	}
	if requester != "" && downloadInfo.Requester != requester {
		return model.DownloadRequest{}, ErrNoDownloadPermission
	}
	return downloadInfo, nil
}

func (s *Service) ApproveDownloadRequest(ctx context.Context, id int, approver string) (model.DownloadRequest, error) {
	downloadInfo, err := s.GetDownloadRequest(ctx, id, "")
	if err != nil {
		return model.DownloadRequest{}, err
	}
	if downloadInfo.Status != model.DownloadRequestStatusPending {
		return model.DownloadRequest{}, ErrDownloadRequestConflict
	}
	now := time.Now()
	expiresAt := now.Add(s.downloadApprovalTTL(ctx))
	updated, err := s.downloadRepo.ApprovePending(ctx, downloadInfo.ID, approver, now, expiresAt)
	if err != nil {
		return model.DownloadRequest{}, err
	}
	if !updated {
		return model.DownloadRequest{}, ErrDownloadRequestConflict
	}
	downloadInfo.Status = model.DownloadRequestStatusApproved
	downloadInfo.Approver = approver
	downloadInfo.ReviewedAt = &now
	downloadInfo.ExpiresAt = &expiresAt
	downloadInfo.Reason = ""
	return downloadInfo, nil
}

func (s *Service) RejectDownloadRequest(ctx context.Context, id int, approver, reason string) (model.DownloadRequest, error) {
	downloadInfo, err := s.GetDownloadRequest(ctx, id, "")
	if err != nil {
		return model.DownloadRequest{}, err
	}
	if downloadInfo.Status != model.DownloadRequestStatusPending {
		return model.DownloadRequest{}, ErrDownloadRequestConflict
	}
	now := time.Now()
	updated, err := s.downloadRepo.RejectPending(ctx, downloadInfo.ID, approver, now, reason)
	if err != nil {
		return model.DownloadRequest{}, err
	}
	if !updated {
		return model.DownloadRequest{}, ErrDownloadRequestConflict
	}
	downloadInfo.Status = model.DownloadRequestStatusRejected
	downloadInfo.Approver = approver
	downloadInfo.ReviewedAt = &now
	downloadInfo.ExpiresAt = nil
	downloadInfo.Reason = reason
	return downloadInfo, nil
}

func (s *Service) ensureDownloadSnapshotWithinAllowedRoots(ctx context.Context, snapshot *payload.FileCheckResult, requestUser string) error {
	if snapshot == nil || snapshot.RealPath == "" {
		return ErrInvalidFilePath
	}
	roots, err := s.downloadAllowedRoots(ctx, requestUser)
	if err != nil {
		return err
	}
	if !downloadResolvedPathWithinRoots(snapshot.RealPath, roots) {
		return ErrNotAllowDownload
	}
	return nil
}

func (s *Service) downloadAllowedRoots(ctx context.Context, requestUser string) ([]string, error) {
	userInfo, err := s.userSvc.Get(ctx, requestUser)
	if err != nil {
		return nil, execution.HandleError(s.logger, err, "file.download_user_lookup")
	}

	homeRoot, err := s.resolveDownloadRoot(ctx, userInfo.Home, requestUser)
	if err != nil {
		return nil, err
	}

	roots := []string{homeRoot}
	extraLocations := s.extraFileLocations(ctx)
	for _, location := range extraLocations {
		if !location.Enabled {
			continue
		}
		if root, ok := s.tryResolveDownloadRoot(ctx, location.Root, requestUser); ok {
			roots = append(roots, root)
		}
	}
	return roots, nil
}

func (s *Service) tryResolveDownloadRoot(ctx context.Context, root, requestUser string) (string, bool) {
	resolved, err := s.resolveDownloadRoot(ctx, root, requestUser)
	return resolved, err == nil
}

func (s *Service) resolveDownloadRoot(ctx context.Context, root, requestUser string) (string, error) {
	cleanRoot, err := cleanDownloadRoot(root)
	if err != nil {
		return "", err
	}

	snapshot, err := s.downloadPathSnapshot(ctx, cleanRoot, requestUser)
	if err != nil {
		return "", err
	}
	if snapshot.RealPath == "" {
		return "", ErrInvalidFilePath
	}
	realRootAbs, err := filepath.Abs(snapshot.RealPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve download root: %w", err)
	}
	return filepath.Clean(realRootAbs), nil
}

func cleanDownloadRoot(root string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve download root: %w", err)
	}
	if realRoot, err := filepath.EvalSymlinks(rootAbs); err == nil {
		return filepath.Clean(realRoot), nil
	}
	return filepath.Clean(rootAbs), nil
}

func downloadResolvedPathWithinRoots(realPath string, roots []string) bool {
	realPath, err := filepath.Abs(realPath)
	if err != nil {
		return false
	}
	realPath = filepath.Clean(realPath)

	return slices.ContainsFunc(roots, func(root string) bool {
		return pathWithinRoot(realPath, root)
	})
}

func pathWithinRoot(target, root string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func fileSnapshotFingerprint(s *payload.FileCheckResult) string {
	return fmt.Sprintf("dev=%d;ino=%d;size=%d;mtime=%d.%09d;ctime=%d.%09d",
		s.Device, s.Inode, s.Size, s.ModTime, s.ModTimeN, s.CTime, s.CTimeN)
}

func (s *Service) downloadPathSnapshot(ctx context.Context, filePath, requestUser string) (payload.FileCheckResult, error) {
	result, err := s.execute(ctx, execution.ActionFileCheck, payload.FileCheck{Path: filePath}, requestUser)
	if err != nil {
		return payload.FileCheckResult{}, execution.HandleError(s.logger, err, "file.download_permission")
	}
	var snapshot payload.FileCheckResult
	if err := json.Unmarshal([]byte(result), &snapshot); err != nil {
		return payload.FileCheckResult{}, fmt.Errorf("invalid file check response from PrivilegedExec: %w", err)
	}
	return snapshot, nil
}

func (s *Service) downloadFileSnapshot(ctx context.Context, filePath, requestUser string) (payload.FileCheckResult, error) {
	snapshot, err := s.downloadPathSnapshot(ctx, filePath, requestUser)
	if err != nil {
		return payload.FileCheckResult{}, err
	}
	if snapshot.IsDir {
		return payload.FileCheckResult{}, ErrInvalidFilePath
	}
	if snapshot.Size > maxDownloadSizeBytes {
		return payload.FileCheckResult{}, fmt.Errorf("%w: max size is %d bytes", ErrDownloadTooLarge, maxDownloadSizeBytes)
	}
	return snapshot, nil
}

func validDownloadStatus(status model.DownloadRequestStatus) bool {
	switch status {
	case model.DownloadRequestStatusPending, model.DownloadRequestStatusApproved, model.DownloadRequestStatusRejected:
		return true
	default:
		return false
	}
}
