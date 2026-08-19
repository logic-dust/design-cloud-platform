package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/common"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/service/settings/schema"

	"gorm.io/gorm"
)

// Service provides platform settings management logic.
// Platform settings are runtime-configurable domain-value pairs stored in the database.
//
// Design: Settings are organized by domain (platform, auth, connection, tunnel, file,
// dashboard, storage, scheduler, license, agent, alert, desktop, cert).
// Concurrency: Per-domain mutexes prevent concurrent updates to the same domain
// from overwriting each other (read-modify-write pattern serialization).
type Service struct {
	settingsRepo      *db.SettingRepo
	nodeRepo          *db.NodeRepo
	audit             *audit.Service
	invalidator       *cache.Invalidator
	domainMutexes     map[schema.Domain]*sync.Mutex
	cache             *cache.TTLCache[schema.Domain, any]
	nodeOverrideCache *cache.TTLCache[string, *schema.NodeSettings]

	// broadcastFn is an optional hook called after a successful settings update
	// to push the latest configuration to connected agents.
	broadcastFn func(ctx context.Context) error
}

// NewService creates a new settings service.
// audit service is required and must not be nil.
func NewService(settingsRepo *db.SettingRepo, auditSvc *audit.Service, invalidator *cache.Invalidator) (*Service, error) {
	if auditSvc == nil {
		return nil, fmt.Errorf("settings: audit service is required")
	}
	s := &Service{
		settingsRepo: settingsRepo,
		audit:        auditSvc,
		invalidator:  invalidator,
		// TTL 仅作为"出带操作"的安全网，不参与受支持路径的失效：
		//   - 本节点 API 写 → UpdateByDomain 内按域 Invalidate(d)，即时生效
		//   - 其他节点写  → Invalidator 统一轮询节拍（InvalidatorPollInterval）→ Clear()
		// TTL 只兜底"绕过服务直改 DB / 未来新增写路径遗漏 Invalidate"的陈旧场景：
		// 24h 内自动重新拉取（365d 约等于永久失效，只能靠重启恢复）。
		cache:             cache.NewTTLCache[schema.Domain, any](24 * time.Hour),
		nodeOverrideCache: cache.NewTTLCache[string, *schema.NodeSettings](60 * time.Second).EnableJanitor(),
	}
	s.domainMutexes = make(map[schema.Domain]*sync.Mutex, len(schema.AllDomains()))
	for _, domain := range schema.AllDomains() {
		s.domainMutexes[domain] = &sync.Mutex{}
	}
	if invalidator != nil {
		// "settings" 是 CacheVersion.Name 缓存失效主题（settings/session/ban/id_mappings 之一），
		// 非 schema.Domain 业务域；它代表整套 settings 缓存，跨节点版本变化时全量 Clear。
		invalidator.Register("settings", func() {
			s.cache.Clear()
			if s.nodeOverrideCache != nil {
				s.nodeOverrideCache.Clear()
			}
		})
	}

	return s, nil
}

// UpdateByDomain 把 raw 叠加到该域的当前值之上，校验后持久化整个域 blob。
//
// 叠加语义（局部更新）：**未携带的字段保持库中现值**，而不是被解码成零值覆盖；
// 携带的字段照常严格校验——越界/不满足跨字段规则 → 整次修改被拒绝（不部分生效）。
// Returns fs.ErrNotExist for unknown domains, fs.ErrInvalid for validation failures.
func (s *Service) UpdateByDomain(ctx context.Context, d schema.Domain, raw []byte) error {
	mu := s.getDomainMutex(d)
	if mu == nil {
		return fmt.Errorf("%w: unknown domain: %s", fs.ErrNotExist, d)
	}

	mu.Lock()
	defer mu.Unlock()

	base, err := s.currentDomainPayload(ctx, d)
	if err != nil {
		return err
	}
	merged, err := schema.MergeDomainPayload(base, raw)
	if err != nil {
		return err
	}

	settingsValue, err := schema.DecodeForStore(d, merged)
	if err != nil {
		return err
	}
	err = s.settingsRepo.Save(ctx, d.String(), settingsValue, func(tx *gorm.DB) error {
		if s.invalidator != nil {
			// "settings" 为缓存失效主题名（CacheVersion.Name），非业务域；
			// 所有业务域（schema.Domain*）的写都 bump 同一主题，供其他节点全量失效。
			return s.invalidator.BumpTx(ctx, tx, "settings")
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.audit.LogSecurity(ctx, "SETTINGS_UPDATE", "success", slog.LevelInfo, map[string]any{
		"domain": d.String(),
	})

	// 本节点写路径明确知道是哪个业务域被改，按域精确失效（写哪个域失效哪个域）。
	// 跨节点路径（Invalidator）只感知 "settings" 主题版本变化、不知道内部哪个域，
	// 因此回调里才用全量 Clear()——两种粒度并存，语义各清晰。
	s.cache.Invalidate(d)

	// Push updated settings to all connected agents (fire-and-forget).
	if s.broadcastFn != nil {
		detachedCtx := context.WithoutCancel(ctx)
		go func() { _ = s.broadcastFn(detachedCtx) }()
	}

	return nil
}

// SetOnUpdated registers a callback that is invoked after every successful
// settings update, enabling real-time push to connected agents.
func (s *Service) SetOnUpdated(fn func(ctx context.Context) error) {
	s.broadcastFn = fn
}

// getDomainMutex returns the per-domain mutex, or nil if the domain is unknown.
func (s *Service) getDomainMutex(d schema.Domain) *sync.Mutex {
	return s.domainMutexes[d]
}

// currentDomainPayload returns the persisted domain JSON as the PUT merge base.
// EnsureDefaults creates every domain before the service starts, so an empty row
// falls back to the schema default only as a defensive guard.
func (s *Service) currentDomainPayload(ctx context.Context, d schema.Domain) ([]byte, error) {
	var stored json.RawMessage
	if err := s.settingsRepo.Load(ctx, d.String(), &stored); err != nil {
		return nil, err
	}
	if len(stored) == 0 {
		defaults, ok := schema.DefaultSettings(d)
		if !ok {
			return nil, fmt.Errorf("settings domain %q has no defaults", d)
		}
		return json.Marshal(defaults)
	}
	return stored, nil
}

// GetByDomain retrieves settings for a specific domain.
func (s *Service) GetByDomain(ctx context.Context, d schema.Domain) (any, error) {
	switch d {
	case schema.DomainPlatform:
		return s.GetPlatformSettings(ctx)
	case schema.DomainFile:
		return s.GetFileSettings(ctx)
	case schema.DomainTunnel:
		return s.GetTunnelSettings(ctx)
	case schema.DomainAuth:
		return s.GetAuthSettings(ctx)
	case schema.DomainConnection:
		return s.GetConnectionSettings(ctx)
	case schema.DomainAgent:
		return s.GetAgentSettings(ctx)
	case schema.DomainStorage:
		return s.GetStorageSettings(ctx)
	case schema.DomainDesktop:
		return s.GetDesktopSettings(ctx)
	case schema.DomainDashboard:
		return s.GetDashboardSettings(ctx)
	case schema.DomainScheduler:
		return s.GetSchedulerSettings(ctx)
	case schema.DomainAlert:
		return s.GetAlertSettings(ctx)
	case schema.DomainCert:
		return s.GetCertSettings(ctx)
	case schema.DomainLicense:
		return s.GetLicenseSettings(ctx)
	default:
		return nil, fmt.Errorf("%w: unknown domain: %s", fs.ErrNotExist, d)
	}
}

// SetNodeRepo injects the Node repository used to resolve per-node settings overrides.
func (s *Service) SetNodeRepo(repo *db.NodeRepo) {
	s.nodeRepo = repo
}

// InvalidateNodeOverride removes the cached override for the specified node.
func (s *Service) InvalidateNodeOverride(nodeID string) {
	if s.nodeOverrideCache != nil && nodeID != "" {
		s.nodeOverrideCache.Invalidate(nodeID)
	}
}

func (s *Service) getNodeOverride(ctx context.Context, nodeID string) (*schema.NodeSettings, error) {
	if s.nodeRepo == nil || nodeID == "" {
		return nil, nil
	}
	return s.nodeOverrideCache.Get(ctx, nodeID, func(ctx context.Context, id string) (*schema.NodeSettings, error) {
		node, err := s.nodeRepo.GetByNodeID(ctx, id)
		if err != nil {
			if errors.Is(err, common.ErrNotFound) {
				return nil, nil // negative cache
			}
			return nil, err // DB error: do not cache
		}
		if len(node.Settings) == 0 {
			return nil, nil // negative cache
		}
		override, err := schema.DecodeNodeSettingsFromMap(node.Settings)
		if err != nil {
			slog.Warn("failed to decode node settings override", "nodeID", id, "error", err)
			return nil, nil
		}
		return override, nil
	})
}

// GetEffectiveAgentSettingsForNode calculates the merged effective settings for the specified node.
func (s *Service) GetEffectiveAgentSettingsForNode(ctx context.Context, nodeID string) (schema.AgentSettings, error) {
	global, err := s.GetAgentSettings(ctx)
	if err != nil {
		return schema.AgentSettings{}, fmt.Errorf("load global agent settings: %w", err)
	}

	override, err := s.getNodeOverride(ctx, nodeID)
	if err != nil {
		return schema.AgentSettings{}, fmt.Errorf("get node override for %q: %w", nodeID, err)
	}
	if override == nil || override.Agent == nil {
		return global, nil
	}
	return schema.MergeAgentSettings(global, override.Agent), nil
}

// GetEffectiveAlertSettingsForNode calculates the merged effective alert settings for the specified node.
func (s *Service) GetEffectiveAlertSettingsForNode(ctx context.Context, nodeID string) (schema.AlertSettings, error) {
	global, err := s.GetAlertSettings(ctx)
	if err != nil {
		return schema.AlertSettings{}, fmt.Errorf("load global alert settings: %w", err)
	}

	override, err := s.getNodeOverride(ctx, nodeID)
	if err != nil {
		return schema.AlertSettings{}, fmt.Errorf("get node override for %q: %w", nodeID, err)
	}
	if override == nil || override.Alert == nil {
		return global, nil
	}
	return schema.MergeAlertSettings(global, override.Alert), nil
}

// GetEffectiveLicenseSettingsForNode calculates the merged effective license settings for the specified node.
func (s *Service) GetEffectiveLicenseSettingsForNode(ctx context.Context, nodeID string) (schema.LicenseSettings, error) {
	global, err := s.GetLicenseSettings(ctx)
	if err != nil {
		return schema.LicenseSettings{}, fmt.Errorf("load global license settings: %w", err)
	}

	override, err := s.getNodeOverride(ctx, nodeID)
	if err != nil {
		return schema.LicenseSettings{}, fmt.Errorf("get node override for %q: %w", nodeID, err)
	}
	if override == nil || override.License == nil {
		return global, nil
	}
	return schema.MergeLicenseSettings(global, override.License), nil
}

// GetAgentSettingsForNode 组装指定节点的 *pb.AgentSettings 供控制面使用
func (s *Service) GetAgentSettingsForNode(ctx context.Context, nodeID string) (*pb.AgentSettings, error) {
	effective, err := s.GetEffectiveAgentSettingsForNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	effectiveLicense, err := s.GetEffectiveLicenseSettingsForNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	diskMounts := make([]*pb.MountTarget, 0, len(effective.Monitor.DiskMounts))
	for _, mt := range effective.Monitor.DiskMounts {
		diskMounts = append(diskMounts, &pb.MountTarget{
			Name:    mt.Name,
			Path:    mt.Path,
			Enabled: mt.Enabled,
			Os:      mt.OS,
		})
	}

	probeIntervalSec := s.GetStorageProbeInterval(ctx)

	return &pb.AgentSettings{
		LauncherPath: effective.LauncherPath,
		LmutilPath:   effectiveLicense.LmutilPath,
		Vnc: &pb.VNCConfig{
			BinDir:       effective.VNC.BinDir,
			ScanInterval: int32(effective.VNC.ScanInterval),
			ScanEnabled:  effective.VNC.ScanEnabled,
			ServerArgs:   effective.VNC.ServerArgs,
		},
		Monitor: &pb.MonitorConfig{
			NvidiaSmiPath:          effective.Monitor.NvidiaSmiPath,
			DiskMounts:             diskMounts,
			ResourceReportEnabled:  effective.Monitor.ResourceReportEnabled,
			ResourceReportInterval: int32(effective.Monitor.ResourceReportInterval),
		},
		Storages:      s.buildSharedStoragesForNode(ctx, nodeID),
		ProbeInterval: int32(probeIntervalSec),
	}, nil
}

func (s *Service) buildSharedStoragesForNode(ctx context.Context, nodeID string) []*pb.StorageConfig {
	storageSettings, err := loadTyped[schema.StorageSettings](ctx, s, schema.DomainStorage)
	if err != nil {
		slog.Warn("failed to load storage settings for node share list",
			"node", nodeID, "error", err)
		return nil
	}
	var result []*pb.StorageConfig
	for _, storage := range storageSettings.Storages {
		if storage.ShouldProbeOnNode(nodeID) {
			result = append(result, &pb.StorageConfig{
				Id:         storage.ID,
				MountPoint: storage.MountPoint,
			})
		}
	}
	return result
}

// GetStorageProbeInterval 获取存储探针扫描周期。
// 尽力读取：DB 读取失败时回退默认，不向外传播错误（默认值足够安全）。
func (s *Service) GetStorageProbeInterval(ctx context.Context) int {
	storageSettings, err := loadTyped[schema.StorageSettings](ctx, s, schema.DomainStorage)
	if err != nil {
		return schema.StorageProbeIntervalDefault
	}
	return storageSettings.EffectiveProbeInterval()
}
