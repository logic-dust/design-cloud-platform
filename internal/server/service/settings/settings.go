package settings

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"design-cloud-engine/internal/audit"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/platform/concurrency"
	"design-cloud-engine/internal/server/cache"

	"design-cloud-engine/internal/contract"
	"gorm.io/gorm"
)

// Service provides platform settings management logic.
// Platform settings are runtime-configurable domain-value pairs stored in the database.
//
// Design: Settings are organized by domain (platform, auth, connection, file,
// overview, storage, scheduler, license, agent, alert, desktop, cert).
// Concurrency: Per-domain mutexes prevent concurrent updates to the same domain
// from overwriting each other (read-modify-write pattern serialization).
type Service struct {
	settingsRepo      *db.SettingRepo
	nodeRepo          *db.NodeRepo
	audit             *audit.Service
	invalidator       *cache.Invalidator
	cacheVersions     *db.CacheVersionRepo
	logger            *slog.Logger
	domainMutexes     map[schema.Domain]*sync.Mutex
	cache             *cache.TTLCache[schema.Domain, any]
	nodeOverrideCache *cache.TTLCache[string, *schema.NodeSettings]
	mu                sync.RWMutex
	subscribers       []func(ctx context.Context) error
	domainSubscribers []func(ctx context.Context, domains []schema.Domain) error
	notifyDomains     map[schema.Domain]struct{}
	notifyDirty       atomic.Bool
	notifyRunning     atomic.Bool
}

// NewService creates a new settings service.
// audit service is required and must not be nil.
// logger may be nil (falls back to slog.Default()).
// cacheVersionRepo and nodeRepo are optional — settings reads may proceed
// without them, but InvalidateCache() and cluster-specific feature flags
// (feature-gated by node config) require them.
func NewService(settingsRepo *db.SettingRepo, logger *slog.Logger, auditSvc *audit.Service, invalidator *cache.Invalidator, cacheVersionRepo *db.CacheVersionRepo, nodeRepo *db.NodeRepo) (*Service, error) {
	if auditSvc == nil {
		return nil, fmt.Errorf("settings: audit service is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{
		settingsRepo:  settingsRepo,
		audit:         auditSvc,
		invalidator:   invalidator,
		cacheVersions: cacheVersionRepo,
		nodeRepo:      nodeRepo,
		logger:        logger.With("component", "settings"),
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
	s.notifyDomains = make(map[schema.Domain]struct{})
	if invalidator != nil {
		invalidator.Register(db.CacheVersionTopicSettings, func() {
			s.cache.Clear()
			if s.nodeOverrideCache != nil {
				s.nodeOverrideCache.Clear()
			}
		})
		invalidator.Register(db.CacheVersionTopicNodeOverrides, func() {
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
// Returns contract.NewNotFoundError for unknown domains; schema validation failures
// (contract.NewValidationError) propagate from DecodeForStore.
func (s *Service) UpdateByDomain(ctx context.Context, d schema.Domain, raw []byte) error {
	mu := s.getDomainMutex(d)
	if mu == nil {
		return contract.NewNotFoundError(fmt.Sprintf("unknown domain: %s", d))
	}

	mu.Lock()
	defer mu.Unlock()

	return s.updateByDomainLocked(ctx, d, raw)
}

// updateByDomainLocked 在调用方已持有 d 域互斥锁的前提下执行域写。
// 供 UpdateByDomain 与需要跨"读-改-写"持锁的局部变更入口（如封禁放行名单）复用。
func (s *Service) updateByDomainLocked(ctx context.Context, d schema.Domain, raw []byte) error {
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
			return s.invalidator.BumpTx(ctx, tx, db.CacheVersionTopicSettings)
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

	s.recordDomainAndSchedule(d)
	return nil
}

// recordDomainAndSchedule records d as a dirty domain and starts a notify pass.
func (s *Service) recordDomainAndSchedule(d schema.Domain) {
	s.mu.Lock()
	s.notifyDomains[d] = struct{}{}
	s.mu.Unlock()
	s.scheduleSubscriberNotify()
}

// scheduleSubscriberNotify coalesces bursty settings writes into serial notify
// passes. Concurrent updates while a pass is running mark dirty and trigger at
// most one follow-up with the latest subscriber set (Broadcast reads current settings).
func (s *Service) scheduleSubscriberNotify() {
	s.notifyDirty.Store(true)
	if !s.notifyRunning.CompareAndSwap(false, true) {
		return
	}
	concurrency.SafeGo(s.logger, "settings-subscriber", func() {
		defer s.notifyRunning.Store(false)
		for s.notifyDirty.Swap(false) {
			s.mu.Lock()
			subs := slices.Clone(s.subscribers)
			domainSubs := slices.Clone(s.domainSubscribers)
			domains := make([]schema.Domain, 0, len(s.notifyDomains))
			for domain := range s.notifyDomains {
				domains = append(domains, domain)
			}
			s.notifyDomains = make(map[schema.Domain]struct{})
			s.mu.Unlock()
			slices.Sort(domains)
			detachedCtx := context.WithoutCancel(context.Background())
			for _, fn := range subs {
				if err := fn(detachedCtx); err != nil {
					s.logger.Warn("settings subscriber failed", "error", err)
				}
			}
			if len(domains) > 0 {
				for _, fn := range domainSubs {
					if err := fn(detachedCtx, domains); err != nil {
						s.logger.Warn("settings domain subscriber failed", "error", err)
					}
				}
			}
		}
		if s.notifyDirty.Load() {
			s.scheduleSubscriberNotify()
		}
	})
}

// Subscribe registers a callback invoked after successful settings updates.
// Bursts are coalesced into serial notify passes (see scheduleSubscriberNotify).
func (s *Service) Subscribe(fn func(ctx context.Context) error) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribers = append(s.subscribers, fn)
}

// SubscribeDomains registers a callback invoked after successful settings updates
// with the domains changed in that notify pass. Empty domain lists are skipped.
func (s *Service) SubscribeDomains(fn func(ctx context.Context, domains []schema.Domain) error) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.domainSubscribers = append(s.domainSubscribers, fn)
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
	case schema.DomainOverview:
		return s.GetOverviewSettings(ctx)
	case schema.DomainScheduler:
		return s.GetSchedulerSettings(ctx)
	case schema.DomainAlert:
		return s.GetAlertSettings(ctx)
	case schema.DomainCert:
		return s.GetCertSettings(ctx)
	case schema.DomainLicense:
		return s.GetLicenseSettings(ctx)
	default:
		return nil, contract.NewNotFoundError(fmt.Sprintf("unknown domain: %s", d))
	}
}

// InvalidateNodeOverride removes the cached override for the specified node (this process only).
func (s *Service) InvalidateNodeOverride(nodeID string) {
	if s.nodeOverrideCache != nil && nodeID != "" {
		s.nodeOverrideCache.Invalidate(nodeID)
	}
}

// BumpNodeOverridesTopicTx increments node_overrides inside an existing write transaction.
func (s *Service) BumpNodeOverridesTopicTx(ctx context.Context, tx *gorm.DB) error {
	if s.cacheVersions == nil {
		return nil
	}
	return s.cacheVersions.BumpTx(ctx, tx, db.CacheVersionTopicNodeOverrides)
}

// BumpNodeOverridesTopic increments node_overrides in its own short transaction
// (e.g. once after a batch of node Settings writes).
func (s *Service) BumpNodeOverridesTopic(ctx context.Context) error {
	if s.cacheVersions == nil {
		return nil
	}
	return s.cacheVersions.Bump(ctx, db.CacheVersionTopicNodeOverrides)
}

func (s *Service) getNodeOverride(ctx context.Context, nodeID string) (*schema.NodeSettings, error) {
	if s.nodeRepo == nil || nodeID == "" {
		return nil, nil
	}
	return s.nodeOverrideCache.Get(ctx, nodeID, func(ctx context.Context, id string) (*schema.NodeSettings, error) {
		node, err := s.nodeRepo.GetByNodeID(ctx, id)
		if err != nil {
			if errors.Is(err, contract.ErrNotFound) {
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

func (s *Service) resolveNodeOverrideForMerge(ctx context.Context, nodeID string, dbOverride *schema.NodeSettings, useDBOverride bool) (*schema.NodeSettings, error) {
	if useDBOverride {
		return dbOverride, nil
	}
	return s.getNodeOverride(ctx, nodeID)
}

// BuildNodeEffectiveSettings merges global domain settings with a node override.
// When useDBOverride is true, dbOverride is authoritative (nil = no override on the row);
// used by GetNodeSettings so override/effective match the same DB read. Otherwise the
// override comes from nodeOverrideCache (Agent push, connection quotas, etc.).
func (s *Service) BuildNodeEffectiveSettings(ctx context.Context, nodeID string, dbOverride *schema.NodeSettings, useDBOverride bool) (schema.NodeEffectiveSettings, error) {
	agent, err := s.effectiveAgentSettings(ctx, nodeID, dbOverride, useDBOverride)
	if err != nil {
		return schema.NodeEffectiveSettings{}, err
	}
	alert, err := s.effectiveAlertSettings(ctx, nodeID, dbOverride, useDBOverride)
	if err != nil {
		return schema.NodeEffectiveSettings{}, err
	}
	license, err := s.effectiveLicenseSettings(ctx, nodeID, dbOverride, useDBOverride)
	if err != nil {
		return schema.NodeEffectiveSettings{}, err
	}
	scheduler, err := s.effectiveSchedulerSettings(ctx, nodeID, dbOverride, useDBOverride)
	if err != nil {
		return schema.NodeEffectiveSettings{}, err
	}
	return schema.NodeEffectiveSettings{
		Agent:     agent,
		Alert:     alert,
		License:   license,
		Scheduler: scheduler,
	}, nil
}

func (s *Service) effectiveAgentSettings(ctx context.Context, nodeID string, dbOverride *schema.NodeSettings, useDBOverride bool) (schema.AgentSettings, error) {
	global, err := s.GetAgentSettings(ctx)
	if err != nil {
		return schema.AgentSettings{}, fmt.Errorf("load global agent settings: %w", err)
	}
	override, err := s.resolveNodeOverrideForMerge(ctx, nodeID, dbOverride, useDBOverride)
	if err != nil {
		return schema.AgentSettings{}, fmt.Errorf("get node override for %q: %w", nodeID, err)
	}
	if override == nil || override.Agent == nil {
		return global, nil
	}
	return schema.MergeAgentSettings(global, override.Agent), nil
}

func (s *Service) effectiveAlertSettings(ctx context.Context, nodeID string, dbOverride *schema.NodeSettings, useDBOverride bool) (schema.AlertSettings, error) {
	global, err := s.GetAlertSettings(ctx)
	if err != nil {
		return schema.AlertSettings{}, fmt.Errorf("load global alert settings: %w", err)
	}
	override, err := s.resolveNodeOverrideForMerge(ctx, nodeID, dbOverride, useDBOverride)
	if err != nil {
		return schema.AlertSettings{}, fmt.Errorf("get node override for %q: %w", nodeID, err)
	}
	if override == nil || override.Alert == nil {
		return global, nil
	}
	return schema.MergeAlertSettings(global, override.Alert), nil
}

func (s *Service) effectiveLicenseSettings(ctx context.Context, nodeID string, dbOverride *schema.NodeSettings, useDBOverride bool) (schema.LicenseSettings, error) {
	global, err := s.GetLicenseSettings(ctx)
	if err != nil {
		return schema.LicenseSettings{}, fmt.Errorf("load global license settings: %w", err)
	}
	override, err := s.resolveNodeOverrideForMerge(ctx, nodeID, dbOverride, useDBOverride)
	if err != nil {
		return schema.LicenseSettings{}, fmt.Errorf("get node override for %q: %w", nodeID, err)
	}
	if override == nil || override.License == nil {
		return global, nil
	}
	return schema.MergeLicenseSettings(global, override.License), nil
}

func (s *Service) effectiveSchedulerSettings(ctx context.Context, nodeID string, dbOverride *schema.NodeSettings, useDBOverride bool) (schema.SchedulerSettings, error) {
	global, err := s.GetSchedulerSettings(ctx)
	if err != nil {
		return schema.SchedulerSettings{}, fmt.Errorf("load global scheduler settings: %w", err)
	}
	override, err := s.resolveNodeOverrideForMerge(ctx, nodeID, dbOverride, useDBOverride)
	if err != nil {
		return schema.SchedulerSettings{}, fmt.Errorf("get node override for %q: %w", nodeID, err)
	}
	if override == nil || override.Scheduler == nil {
		return global, nil
	}
	return schema.MergeSchedulerSettings(global, override.Scheduler), nil
}

// GetEffectiveSchedulerSettingsForNode calculates the merged effective scheduler settings for the specified node.
func (s *Service) GetEffectiveSchedulerSettingsForNode(ctx context.Context, nodeID string) (schema.SchedulerSettings, error) {
	return s.effectiveSchedulerSettings(ctx, nodeID, nil, false)
}

// GetEffectiveAgentSettingsForNode calculates the merged effective settings for the specified node.
func (s *Service) GetEffectiveAgentSettingsForNode(ctx context.Context, nodeID string) (schema.AgentSettings, error) {
	return s.effectiveAgentSettings(ctx, nodeID, nil, false)
}

// GetEffectiveAlertSettingsForNode calculates the merged effective alert settings for the specified node.
func (s *Service) GetEffectiveAlertSettingsForNode(ctx context.Context, nodeID string) (schema.AlertSettings, error) {
	return s.effectiveAlertSettings(ctx, nodeID, nil, false)
}

// GetEffectiveLicenseSettingsForNode calculates the merged effective license settings for the specified node.
func (s *Service) GetEffectiveLicenseSettingsForNode(ctx context.Context, nodeID string) (schema.LicenseSettings, error) {
	return s.effectiveLicenseSettings(ctx, nodeID, nil, false)
}

// GetAgentSettingsForNode 组装指定节点的 *pb.AgentSettings 供控制面使用
func (s *Service) GetAgentSettingsForNode(ctx context.Context, nodeID string) (*pb.AgentSettings, error) {
	override, err := s.getNodeOverride(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	effective, err := s.effectiveAgentSettings(ctx, nodeID, override, true)
	if err != nil {
		return nil, err
	}
	effectiveLicense, err := s.effectiveLicenseSettings(ctx, nodeID, override, true)
	if err != nil {
		return nil, err
	}
	effectiveScheduler, err := s.effectiveSchedulerSettings(ctx, nodeID, override, true)
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
		ExecBinPath:        effective.ExecBinPath,
		LmutilPath:         effectiveLicense.LmutilPath,
		SchedulerEnvScript: effectiveScheduler.EnvScript,
		Vnc: &pb.VNCConfig{
			BinDir:       effective.VNC.BinDir,
			ScanInterval: int32(effective.VNC.ScanInterval),
			ScanEnabled:  effective.VNC.ScanEnabled,
			ServerArgs:   effective.VNC.ServerArgs,
		},
		Monitor: &pb.MonitorConfig{
			NvidiaSmiPath:   effective.Monitor.NvidiaSmiPath,
			DiskMounts:      diskMounts,
			MetricsEnabled:  effective.Monitor.MetricsEnabled,
			MetricsInterval: int32(effective.Monitor.MetricsInterval),
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
