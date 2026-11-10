package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/push"
)

var (
	ErrUnsupportedJobControlOperation = errors.New("unsupported job control operation")
	ErrJobNotFound                    = errors.New("job not found")
)

// MaxBatchControlJobs bounds the number of scheduler CLI operations in one request.
const MaxBatchControlJobs = 100

type Service struct {
	execSvc     execution.Executor
	settingsSvc SettingsReader
	logger      *slog.Logger
	publisher   push.Publisher

	// mu guards the driver state below. The driver follows settings.scheduler.type:
	// driver() re-resolves it on every call and swaps it when the type changes.
	// With settingsSvc == nil the driver is fixed (unit tests).
	mu      sync.RWMutex
	ops     Scheduler
	opsType schema.JobScheduler
	meta    SchedulerMeta

	// jobCache avoids hammering the external scheduler CLI on every jobs list API call.
	// Key = cluster or per-user query (+ admin queue/node filters) + scheduler type，
	// TTL 跟随 scheduler 域 JobCacheTTL。
	jobCache *cache.TTLCache[string, JobListResult]
}

// jobCacheMaxEntries 是作业列表缓存的条目硬顶。
// 键由 ListJobs 的 JobListQuery（空 User = 全集群，否则按属主）与 queue|node 过滤拼成。
// 没有自然边界；此上限给内存占用一个与 TTL 配置无关的封顶值，
// 正常使用（活跃用户 × 少量过滤组合）远低于它。连接列表直接读库，不走这份缓存。
const jobCacheMaxEntries = 4096

func NewService(execSvc execution.Executor, settingsSvc SettingsReader, logger *slog.Logger, publisher push.Publisher) (*Service, error) {
	if execSvc == nil {
		return nil, fmt.Errorf("scheduler: execution service is required")
	}
	if settingsSvc == nil {
		return nil, fmt.Errorf("scheduler: settings service is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("scheduler: logger is required")
	}
	if publisher == nil {
		publisher = push.NoopPublisher{}
	}
	return &Service{
		execSvc:     execSvc,
		settingsSvc: settingsSvc,
		logger:      logger,
		publisher:   publisher,
		jobCache: cache.NewTTLCache[string, JobListResult](
			// 缓存级 TTL = 代码默认值，作为 settings 不可读/未配置时的唯一兜底来源
			// （调用方无需自备默认值，传 0 即走这里）。
			time.Duration(schema.SchedulerJobCacheTTLDefault) * time.Second).
			SetMaxEntries(jobCacheMaxEntries).
			EnableJanitor(),
	}, nil
}

// driver returns the driver and metadata for the scheduler type currently configured in
// settings.scheduler.type, swapping the driver (and dropping cached job lists) on change.
func (s *Service) driver(ctx context.Context) (Scheduler, SchedulerMeta, error) {
	if s.settingsSvc == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.ops, s.meta, nil
	}
	st, err := s.settingsSvc.GetSchedulerSettings(ctx)
	if err != nil {
		return nil, SchedulerMeta{}, fmt.Errorf("scheduler: load settings: %w", err)
	}

	s.mu.RLock()
	ops, meta, cur := s.ops, s.meta, s.opsType
	s.mu.RUnlock()
	if ops != nil && cur == st.Type {
		return ops, meta, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ops != nil && s.opsType == st.Type {
		return s.ops, s.meta, nil
	}
	drv := newDriver(st.Type)
	if drv == nil {
		return nil, SchedulerMeta{}, fmt.Errorf("scheduler: unsupported scheduler %q", st.Type)
	}
	meta = drv.Meta()
	meta.Scheduler = st.Type
	s.ops, s.opsType, s.meta = drv, st.Type, meta
	if s.jobCache != nil {
		s.jobCache.Clear()
	}
	return drv, meta, nil
}

// lastDriver returns the most recently resolved driver and metadata without consulting
// settings. It serves context-free helpers that only run on data produced by driver().
func (s *Service) lastDriver() (Scheduler, SchedulerMeta) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ops != nil && s.meta.Keys.Status == "" {
		return s.ops, s.ops.Meta()
	}
	return s.ops, s.meta
}

// Meta returns a detached copy of the metadata of the currently configured scheduler
// (Scheduler, Fields, Keys, Actions).
func (s *Service) Meta(ctx context.Context) (SchedulerMeta, error) {
	_, meta, err := s.driver(ctx)
	if err != nil {
		return SchedulerMeta{}, err
	}
	return detachedMeta(meta), nil
}
