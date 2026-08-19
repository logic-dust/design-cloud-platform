package license

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/service/settings/schema"
)

type settingsService interface {
	GetLicenseSettings(ctx context.Context) (schema.LicenseSettings, error)
}

type nodeChecker interface {
	GetByNodeID(ctx context.Context, nodeID string) (model.Node, error)
}

// Option configures optional parameters on license.Service.
type Option func(*Service)

// WithNodeRepo configures a *db.NodeRepo as the node checker for server affinity.
func WithNodeRepo(repo *db.NodeRepo) Option {
	return func(s *Service) {
		s.nodeChecker = repo
	}
}

// Service 提供许可证管理功能。
// 通过 Executor 接口与集群通信体系交互，运行态状态与 features 经过进程内 TTLCache 节流。
type Service struct {
	execSvc       execution.Executor
	licenseRepo   *db.LicenseRepo
	settingsSvc   settingsService
	nodeChecker   nodeChecker
	logger        *slog.Logger
	statusCache   *cache.TTLCache[uint, model.LicenseServiceStatus]
	featuresCache *cache.TTLCache[uint, []featureInfo]
}

// NewService 创建 license 服务实例
func NewService(execSvc execution.Executor, licenseRepo *db.LicenseRepo, settingsSvc settingsService, logger *slog.Logger, opts ...Option) *Service {
	s := &Service{
		execSvc:       execSvc,
		licenseRepo:   licenseRepo,
		settingsSvc:   settingsSvc,
		logger:        logger,
		statusCache:   cache.NewTTLCache[uint, model.LicenseServiceStatus](schema.LicenseStatusCacheTTLDefault * time.Second).EnableJanitor(),
		featuresCache: cache.NewTTLCache[uint, []featureInfo](schema.LicenseFeaturesCacheTTLDefault * time.Second).EnableJanitor(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) statusCacheTTL(ctx context.Context) time.Duration {
	if s.settingsSvc == nil {
		return 0
	}
	settings, err := s.settingsSvc.GetLicenseSettings(ctx)
	if err != nil {
		return 0
	}
	return time.Duration(settings.StatusCacheTTL) * time.Second
}

func (s *Service) featuresCacheTTL(ctx context.Context) time.Duration {
	if s.settingsSvc == nil {
		return 0
	}
	settings, err := s.settingsSvc.GetLicenseSettings(ctx)
	if err != nil {
		return 0
	}
	return time.Duration(settings.FeaturesCacheTTL) * time.Second
}

func (s *Service) overlayStatus(lic *model.LicenseService) {
	if status, ok := s.statusCache.GetIfPresent(lic.ID); ok {
		lic.Status = status
	} else {
		lic.Status = model.LicenseServiceStatusUnknown
	}
}

func (s *Service) validateForm(ctx context.Context, form *model.LicenseServiceBase) error {
	if strings.TrimSpace(form.Name) == "" {
		return fmt.Errorf("%w: license service name cannot be empty", fs.ErrInvalid)
	}
	if len(form.Name) > 64 {
		return fmt.Errorf("%w: license service name cannot exceed 64 characters", fs.ErrInvalid)
	}
	if form.Type != model.LicenseTypeFlexlm {
		return fmt.Errorf("%w: license service type must be %q", fs.ErrInvalid, model.LicenseTypeFlexlm)
	}
	if strings.TrimSpace(form.Address) == "" {
		return fmt.Errorf("%w: license service address cannot be empty", fs.ErrInvalid)
	}
	if form.Server != "" && s.nodeChecker != nil {
		if _, err := s.nodeChecker.GetByNodeID(ctx, form.Server); err != nil {
			if errors.Is(err, common.ErrNotFound) {
				return fmt.Errorf("%w: server node %q not found", fs.ErrInvalid, form.Server)
			}
			return fmt.Errorf("check server node %q: %w", form.Server, err)
		}
	}
	return nil
}

// GetStatus queries the current license service status with TTLCache.
func (s *Service) GetStatus(ctx context.Context, licServiceID uint) (string, error) {
	licInfo, err := s.licenseRepo.Get(ctx, licServiceID)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return "", fmt.Errorf("%w: license service %d not found", common.ErrNotFound, licServiceID)
		}
		return "", fmt.Errorf("get license service %d: %w", licServiceID, err)
	}
	if !licInfo.Enabled {
		return "", fmt.Errorf("%w: license service %d not found", common.ErrNotFound, licServiceID)
	}

	status, err := s.statusCache.GetWithTTL(ctx, licServiceID, s.statusCacheTTL(ctx), func(ctx context.Context, id uint) (model.LicenseServiceStatus, error) {
		return s.Status(ctx, &licInfo)
	})
	if err != nil {
		return "", err
	}
	return string(status), nil
}

// Status 查询许可证服务状态
func (s *Service) Status(ctx context.Context, licInfo *model.LicenseService) (model.LicenseServiceStatus, error) {
	switch licInfo.Type {
	case model.LicenseTypeFlexlm:
		return s.flexLMStatus(ctx, licInfo)
	default:
		return "", fmt.Errorf("%w: unsupported license type %q", fs.ErrInvalid, licInfo.Type)
	}
}

// ============================================================================
// License Feature Operations
// ============================================================================

// ListFeatures 获取许可证功能列表（带 TTLCache 节流）
func (s *Service) ListFeatures(ctx context.Context, licServiceID uint) ([]featureInfo, error) {
	licService, err := s.licenseRepo.Get(ctx, licServiceID)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return nil, fmt.Errorf("%w: license service %d not found", common.ErrNotFound, licServiceID)
		}
		return nil, fmt.Errorf("get license service %d: %w", licServiceID, err)
	}
	if !licService.Enabled {
		return nil, fmt.Errorf("%w: license service %d not found", common.ErrNotFound, licServiceID)
	}

	switch licService.Type {
	case model.LicenseTypeFlexlm:
		return s.featuresCache.GetWithTTL(ctx, licServiceID, s.featuresCacheTTL(ctx), func(ctx context.Context, id uint) ([]featureInfo, error) {
			return s.flexLMListFeatures(ctx, &licService)
		})
	default:
		return nil, fmt.Errorf("%w: unsupported license type %q", fs.ErrInvalid, licService.Type)
	}
}

// ============================================================================
// CRUD Operations
// ============================================================================

// List returns all enabled license services with status overlay (does not trigger lmstat)
func (s *Service) List(ctx context.Context) ([]model.LicenseService, error) {
	services, err := s.licenseRepo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list license services: %w", err)
	}
	var out []model.LicenseService
	for _, svc := range services {
		if !svc.Enabled {
			continue
		}
		s.overlayStatus(&svc)
		out = append(out, svc)
	}
	return out, nil
}

// Get returns an enabled license service by ID with status overlay
func (s *Service) Get(ctx context.Context, id uint) (model.LicenseService, error) {
	licService, err := s.licenseRepo.Get(ctx, id)
	if err != nil {
		return model.LicenseService{}, fmt.Errorf("get license service %d: %w", id, err)
	}
	if !licService.Enabled {
		return model.LicenseService{}, fmt.Errorf("license service %d: %w", id, common.ErrNotFound)
	}
	s.overlayStatus(&licService)
	return licService, nil
}

// AdminList returns all license services (including disabled) with status overlay for admins
func (s *Service) AdminList(ctx context.Context) ([]model.LicenseService, error) {
	services, err := s.licenseRepo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list license services: %w", err)
	}
	for i := range services {
		s.overlayStatus(&services[i])
	}
	return services, nil
}

// AdminGet returns a license service by ID (including disabled) with status overlay for admins
func (s *Service) AdminGet(ctx context.Context, id uint) (model.LicenseService, error) {
	licService, err := s.licenseRepo.Get(ctx, id)
	if err != nil {
		return model.LicenseService{}, fmt.Errorf("get license service %d: %w", id, err)
	}
	s.overlayStatus(&licService)
	return licService, nil
}

// CreateFromInput creates a new license service from writable input fields.
func (s *Service) CreateFromInput(ctx context.Context, form model.LicenseServiceBase) (model.LicenseService, error) {
	if err := s.validateForm(ctx, &form); err != nil {
		return model.LicenseService{}, err
	}
	var licService model.LicenseService
	applyLicenseServiceForm(&licService, form)
	if err := s.licenseRepo.Save(ctx, &licService); err != nil {
		return model.LicenseService{}, fmt.Errorf("create license service: %w", err)
	}
	s.overlayStatus(&licService)
	return licService, nil
}

// UpdateFromInput updates an existing license service with writable input fields only.
func (s *Service) UpdateFromInput(ctx context.Context, id uint, form model.LicenseServiceBase) (model.LicenseService, error) {
	if err := s.validateForm(ctx, &form); err != nil {
		return model.LicenseService{}, err
	}
	licService, err := s.licenseRepo.Get(ctx, id)
	if err != nil {
		return model.LicenseService{}, fmt.Errorf("get license service %d for update: %w", id, err)
	}
	applyLicenseServiceForm(&licService, form)
	if err := s.licenseRepo.Save(ctx, &licService); err != nil {
		return model.LicenseService{}, fmt.Errorf("update license service %d: %w", id, err)
	}
	s.statusCache.Invalidate(id)
	s.featuresCache.Invalidate(id)
	s.overlayStatus(&licService)
	return licService, nil
}

// Delete deletes a license service by ID and invalidates cache entries
func (s *Service) Delete(ctx context.Context, id uint) error {
	if err := s.licenseRepo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete license service %d: %w", id, err)
	}
	s.statusCache.Invalidate(id)
	s.featuresCache.Invalidate(id)
	return nil
}

func applyLicenseServiceForm(lic *model.LicenseService, form model.LicenseServiceBase) {
	lic.LicenseServiceBase = form
}
