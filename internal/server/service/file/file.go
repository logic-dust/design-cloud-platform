package file

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/settings/schema"
	"design-cloud-engine/internal/server/service/user"
)

var ErrFileDisabled = common.NewForbiddenError("file service disabled")

type Service struct {
	fileRepo    *db.FileRepo
	logger      *slog.Logger
	execSvc     execution.Executor
	userSvc     *user.Service
	settingsSvc *settings.Service
	cfg         Config
	streamExec  *StreamExecutor

	downloadTokensMu sync.Mutex
	downloadTokens   map[string]downloadTokenEntry
	lastTokenCleanup time.Time
}

type Config struct {
	GetLauncherPath      func(context.Context) string
	GetDesktopReleaseDir func(context.Context) string
}

const defaultFileActionTimeout = 25 * time.Second

func NewService(execSvc execution.Executor, userSvc *user.Service, settingsSvc *settings.Service, cfg Config, fileRepo *db.FileRepo, logger *slog.Logger) *Service {
	return &Service{
		fileRepo:    fileRepo,
		logger:      logger,
		execSvc:     execSvc,
		userSvc:     userSvc,
		settingsSvc: settingsSvc,
		cfg:         cfg,
		streamExec:  newStreamExecutor(cfg.GetLauncherPath),

		downloadTokens: make(map[string]downloadTokenEntry),
	}
}

func (s *Service) desktopReleaseDir(ctx context.Context) string {
	if s.cfg.GetDesktopReleaseDir != nil {
		if dir := s.cfg.GetDesktopReleaseDir(ctx); dir != "" {
			return dir
		}
	}
	return "data/releases/desktop"
}

// IsEnabled reports whether the file service is enabled by global settings.
func (s *Service) IsEnabled(ctx context.Context) bool {
	if s.settingsSvc == nil {
		return true
	}
	fileSetting, err := s.settingsSvc.GetFileSettings(ctx)
	if err != nil {
		return true
	}
	return fileSetting.Enabled
}

func (s *Service) downloadPolicy(ctx context.Context) DownloadPermission {
	if s.settingsSvc == nil {
		return DownloadPermissionAllow
	}
	fileSetting, err := s.settingsSvc.GetFileSettings(ctx)
	if err != nil {
		return DownloadPermissionAllow
	}
	if !fileSetting.Enabled || !fileSetting.DownloadEnabled {
		return DownloadPermissionDeny
	}
	if fileSetting.RequiresApproval {
		return DownloadPermissionRequest
	}
	return DownloadPermissionAllow
}

func (s *Service) defaultUploadMaxSizeBytes(ctx context.Context) int64 {
	if fileSetting, err := s.settingsSvc.GetFileSettings(ctx); err == nil && fileSetting.UploadMaxSizeBytes > 0 {
		return fileSetting.UploadMaxSizeBytes
	}
	return schema.UploadMaxSizeBytesDefault
}

func (s *Service) downloadApprovalTTL(ctx context.Context) time.Duration {
	if fileSetting, err := s.settingsSvc.GetFileSettings(ctx); err == nil && fileSetting.DownloadApprovalTTL > 0 {
		return time.Duration(fileSetting.DownloadApprovalTTL) * time.Second
	}
	return 86400 * time.Second
}

// execute is a helper to run file actions via dc-launcher.
// When s.execSvc is non-nil (e.g. injected in tests), it delegates to execution.Run(ctx, s.execSvc, req).
// Otherwise, it runs directly via s.streamExec on the local host.
func (s *Service) execute(ctx context.Context, actionType execution.ActionName, payload any, runUser string) (string, error) {
	executionID := fmt.Sprintf("%s-%d", actionType, time.Now().UnixNano())

	opts := []execution.RequestOption{
		execution.WithTargetUser(runUser),
		execution.WithPayload(payload),
		execution.WithTimeout(defaultFileActionTimeout),
	}

	req := execution.NewExecuteRequest(actionType, executionID, opts...)
	if s.execSvc != nil {
		return execution.Run(ctx, s.execSvc, req)
	}
	return s.streamExec.Execute(ctx, req)
}

// CreateDir creates a directory.
func (s *Service) CreateDir(ctx context.Context, dirPath, runUser string) error {
	if _, err := s.execute(ctx, execution.ActionFileMkdir, payload.FileMkdir{Dir: dirPath}, runUser); err != nil {
		return execution.HandleError(s.logger, err, "file.mkdir")
	}
	return nil
}

func (s *Service) CreateLocationDir(ctx context.Context, locationName, parentRelPath, name, runUser string) error {
	if err := s.ensureWritableLocation(ctx, locationName, runUser); err != nil {
		return err
	}
	if err := validateEntryName(name); err != nil {
		return err
	}
	cleanParent, err := normalizeLocationPath(parentRelPath)
	if err != nil {
		return err
	}
	parentPath, err := s.ResolveLocationPath(ctx, locationName, cleanParent, runUser)
	if err != nil {
		return err
	}
	return s.CreateDir(ctx, filepath.Join(parentPath, name), runUser)
}

// RenameFile renames a file.
func (s *Service) RenameFile(ctx context.Context, origPath, newName, runUser string) error {
	_, err := s.execute(ctx, execution.ActionFileRename, payload.FileRename{Src: origPath, Name: newName}, runUser)
	return execution.HandleError(s.logger, err, "file.rename")
}

func (s *Service) RenameLocationPath(ctx context.Context, locationName, relPath, newName, runUser string) error {
	if err := s.ensureWritableLocation(ctx, locationName, runUser); err != nil {
		return err
	}
	cleanRel, err := normalizeLocationPath(relPath)
	if err != nil {
		return err
	}
	if cleanRel == "" {
		return fmt.Errorf("%w: path is required", fs.ErrInvalid)
	}
	if err := validateEntryName(newName); err != nil {
		return err
	}
	srcPath, err := s.ResolveLocationPath(ctx, locationName, cleanRel, runUser)
	if err != nil {
		return err
	}
	return s.RenameFile(ctx, srcPath, newName, runUser)
}
