package file

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/common/crypto"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
)

const (
	maxDownloadSizeBytes         int64 = 100 * 1024 * 1024 * 1024
	maxDownloadTokens                  = 4096
	downloadTokenCleanupInterval       = 30 * time.Second
	downloadTicketTTL                  = 30 * time.Second
)

type downloadTokenEntry struct {
	Requester       string
	Path            string
	FileFingerprint string
	CreatedAt       time.Time
	ExpiresAt       time.Time
	ApprovalExpires time.Time
}

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
		return "", fmt.Errorf("%w: location is required", fs.ErrInvalid)
	}
	cleanRel, err := normalizeLocationPath(relPath)
	if err != nil {
		return "", err
	}
	if cleanRel == "" {
		return "", fmt.Errorf("%w: path is required", fs.ErrInvalid)
	}
	return s.ResolveLocationPath(ctx, locationName, cleanRel, requestUser)
}

// CreateAllowedDownloadToken creates a short-lived one-time in-memory download token.
func (s *Service) CreateAllowedDownloadToken(ctx context.Context, filePath, requestUser string) (DownloadToken, error) {
	permission := s.CheckDownloadPermission(ctx, requestUser)
	if permission == DownloadPermissionDeny {
		return DownloadToken{}, ErrNoDownloadPermission
	}
	if permission == DownloadPermissionRequest {
		return DownloadToken{}, ErrDownloadApprovalRequired
	}

	snapshot, err := s.downloadFileSnapshot(ctx, filePath, requestUser)
	if err != nil {
		return DownloadToken{}, err
	}
	if err := s.ensureDownloadSnapshotWithinAllowedRoots(ctx, &snapshot, requestUser); err != nil {
		return DownloadToken{}, err
	}
	return s.createDownloadToken(ctx, downloadTokenEntry{
		Path:            filePath,
		FileFingerprint: fileSnapshotFingerprint(&snapshot),
		Requester:       requestUser,
	})
}

func (s *Service) CreateAllowedLocationDownloadToken(ctx context.Context, locationName, relPath, requestUser string) (DownloadToken, error) {
	filePath, err := s.ResolveDownloadPath(ctx, locationName, relPath, requestUser)
	if err != nil {
		return DownloadToken{}, err
	}
	return s.CreateAllowedDownloadToken(ctx, filePath, requestUser)
}

func (s *Service) createDownloadToken(ctx context.Context, entry downloadTokenEntry) (DownloadToken, error) {
	now := time.Now()
	expiresAt := now.Add(downloadTicketTTL)
	tokenStr := "dl_" + crypto.GenSecret(32)
	entry.CreatedAt = now
	entry.ExpiresAt = expiresAt
	s.downloadTokensMu.Lock()
	if now.Sub(s.lastTokenCleanup) > downloadTokenCleanupInterval || len(s.downloadTokens) >= maxDownloadTokens {
		s.cleanupExpiredDownloadTokensLocked(now)
		s.lastTokenCleanup = now
	}
	for len(s.downloadTokens) >= maxDownloadTokens {
		if !s.dropOldestDownloadTokenLocked() {
			break
		}
	}
	s.downloadTokens[tokenStr] = entry
	s.downloadTokensMu.Unlock()
	return DownloadToken{Token: tokenStr, ExpiresAt: expiresAt}, nil
}

func (s *Service) consumeDownloadToken(token, requester string) (downloadTokenEntry, bool) {
	now := time.Now()
	s.downloadTokensMu.Lock()
	defer s.downloadTokensMu.Unlock()
	entry, ok := s.downloadTokens[token]
	if !ok {
		return downloadTokenEntry{}, false
	}
	if now.After(entry.ExpiresAt) {
		delete(s.downloadTokens, token)
		return downloadTokenEntry{}, false
	}
	if entry.Requester != requester {
		return downloadTokenEntry{}, false
	}
	delete(s.downloadTokens, token)
	return entry, true
}

func (s *Service) cleanupExpiredDownloadTokensLocked(now time.Time) {
	for token, entry := range s.downloadTokens {
		if now.After(entry.ExpiresAt) {
			delete(s.downloadTokens, token)
		}
	}
}

func (s *Service) dropOldestDownloadTokenLocked() bool {
	var oldestToken string
	var oldestTime time.Time
	found := false
	for token, entry := range s.downloadTokens {
		if !found || entry.CreatedAt.Before(oldestTime) {
			oldestToken = token
			oldestTime = entry.CreatedAt
			found = true
		}
	}
	if oldestToken == "" {
		return false
	}
	delete(s.downloadTokens, oldestToken)
	return true
}

// GetAllowedDownloadFile consumes a short-lived in-memory download token and returns the file path and size.
func (s *Service) GetAllowedDownloadFile(ctx context.Context, token, downloadUser string) (string, int64, error) {
	entry, ok := s.consumeDownloadToken(token, downloadUser)
	if !ok {
		return "", 0, ErrInvalidDownloadToken
	}
	if s.CheckDownloadPermission(ctx, downloadUser) == DownloadPermissionDeny {
		return "", 0, ErrNoDownloadPermission
	}
	if !entry.ApprovalExpires.IsZero() && time.Now().After(entry.ApprovalExpires) {
		return "", 0, ErrDownloadRequestExpired
	}
	snapshot, err := s.downloadFileSnapshot(ctx, entry.Path, downloadUser)
	if err != nil {
		return "", 0, err
	}
	if err := s.ensureDownloadSnapshotWithinAllowedRoots(ctx, &snapshot, downloadUser); err != nil {
		return "", 0, err
	}
	// Reject tickets whose target was replaced or modified after ticket creation.
	if entry.FileFingerprint == "" || entry.FileFingerprint != fileSnapshotFingerprint(&snapshot) {
		return "", 0, ErrDownloadFileChanged
	}
	return entry.Path, snapshot.Size, nil
}

// SubmitDownloadRequest creates a pending download request and pins the current file fingerprint.
func (s *Service) SubmitDownloadRequest(ctx context.Context, filePath, requestUser string) (model.Download, error) {
	permission := s.CheckDownloadPermission(ctx, requestUser)
	if permission == DownloadPermissionDeny {
		return model.Download{}, ErrNoDownloadPermission
	}
	if permission == DownloadPermissionAllow {
		return model.Download{}, ErrDownloadApprovalNotNeeded
	}

	snapshot, err := s.downloadFileSnapshot(ctx, filePath, requestUser)
	if err != nil {
		return model.Download{}, err
	}
	if err := s.ensureDownloadSnapshotWithinAllowedRoots(ctx, &snapshot, requestUser); err != nil {
		return model.Download{}, err
	}

	// Approval records pin the current file identity without reading the whole file.
	download := model.Download{
		Path:            filePath,
		FileFingerprint: fileSnapshotFingerprint(&snapshot),
		Size:            snapshot.Size,
		Requester:       requestUser,
		Status:          model.DownloadStatusPending,
	}
	if err := s.fileRepo.AddDownloadInfo(ctx, &download); err != nil {
		return model.Download{}, err
	}
	return download, nil
}

func (s *Service) SubmitLocationDownloadRequest(ctx context.Context, locationName, relPath, requestUser string) (model.Download, error) {
	filePath, err := s.ResolveDownloadPath(ctx, locationName, relPath, requestUser)
	if err != nil {
		return model.Download{}, err
	}
	return s.SubmitDownloadRequest(ctx, filePath, requestUser)
}

func (s *Service) ListDownloadRequests(ctx context.Context, status model.DownloadStatus, requester string) ([]model.Download, error) {
	if status != 0 && !validDownloadStatus(status) {
		return nil, ErrInvalidDownloadStatus
	}
	return s.fileRepo.ListDownloadRequests(ctx, status, requester)
}

func (s *Service) GetDownloadRequest(ctx context.Context, id int, requester string) (model.Download, error) {
	downloadInfo, err := s.fileRepo.GetDownloadByID(ctx, id)
	if err != nil {
		return model.Download{}, ErrDownloadRequestNotFound
	}
	if requester != "" && downloadInfo.Requester != requester {
		return model.Download{}, ErrNoDownloadPermission
	}
	return downloadInfo, nil
}

func (s *Service) ApproveDownloadRequest(ctx context.Context, id int, approver string) (model.Download, error) {
	downloadInfo, err := s.GetDownloadRequest(ctx, id, "")
	if err != nil {
		return model.Download{}, err
	}
	if downloadInfo.Status != model.DownloadStatusPending {
		return model.Download{}, ErrDownloadRequestConflict
	}
	now := time.Now()
	downloadInfo.Status = model.DownloadStatusApproved
	downloadInfo.Approver = approver
	downloadInfo.ReviewedAt = &now
	downloadInfo.ExpiresAt = new(now.Add(s.downloadApprovalTTL(ctx)))
	downloadInfo.Reason = ""
	if err := s.fileRepo.SaveDownloadRequest(ctx, &downloadInfo); err != nil {
		return model.Download{}, err
	}
	return downloadInfo, nil
}

func (s *Service) RejectDownloadRequest(ctx context.Context, id int, approver, reason string) (model.Download, error) {
	downloadInfo, err := s.GetDownloadRequest(ctx, id, "")
	if err != nil {
		return model.Download{}, err
	}
	if downloadInfo.Status != model.DownloadStatusPending {
		return model.Download{}, ErrDownloadRequestConflict
	}
	downloadInfo.Status = model.DownloadStatusRejected
	downloadInfo.Approver = approver
	downloadInfo.ReviewedAt = new(time.Now())
	downloadInfo.ExpiresAt = nil
	downloadInfo.Reason = reason
	if err := s.fileRepo.SaveDownloadRequest(ctx, &downloadInfo); err != nil {
		return model.Download{}, err
	}
	return downloadInfo, nil
}

func (s *Service) CreateApprovedDownloadToken(ctx context.Context, id int, requester string) (DownloadToken, error) {
	if s.CheckDownloadPermission(ctx, requester) == DownloadPermissionDeny {
		return DownloadToken{}, ErrNoDownloadPermission
	}

	downloadInfo, err := s.GetDownloadRequest(ctx, id, requester)
	if err != nil {
		return DownloadToken{}, err
	}
	if downloadInfo.Status == model.DownloadStatusRejected {
		return DownloadToken{}, ErrDownloadRequestRejected
	}
	if downloadInfo.Status != model.DownloadStatusApproved {
		return DownloadToken{}, ErrDownloadRequestNotApproved
	}
	if downloadInfo.ExpiresAt != nil && time.Now().After(*downloadInfo.ExpiresAt) {
		return DownloadToken{}, ErrDownloadRequestExpired
	}
	snapshot, err := s.downloadFileSnapshot(ctx, downloadInfo.Path, requester)
	if err != nil {
		return DownloadToken{}, err
	}
	if err := s.ensureDownloadSnapshotWithinAllowedRoots(ctx, &snapshot, requester); err != nil {
		return DownloadToken{}, err
	}
	// Token issuance is bound to the original request fingerprint, not the post-approval file state.
	if downloadInfo.FileFingerprint == "" || downloadInfo.FileFingerprint != fileSnapshotFingerprint(&snapshot) {
		return DownloadToken{}, ErrDownloadFileChanged
	}
	var approvalExpires time.Time
	if downloadInfo.ExpiresAt != nil {
		approvalExpires = *downloadInfo.ExpiresAt
	}
	return s.createDownloadToken(ctx, downloadTokenEntry{
		Requester:       requester,
		Path:            downloadInfo.Path,
		FileFingerprint: downloadInfo.FileFingerprint,
		ApprovalExpires: approvalExpires,
	})
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

	for _, root := range roots {
		if pathWithinRoot(realPath, root) {
			return true
		}
	}
	return false
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
		return payload.FileCheckResult{}, fmt.Errorf("invalid file check response from launcher: %w", err)
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

func validDownloadStatus(status model.DownloadStatus) bool {
	switch status {
	case model.DownloadStatusPending, model.DownloadStatusApproved, model.DownloadStatusRejected:
		return true
	default:
		return false
	}
}
