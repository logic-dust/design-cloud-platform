package scheduler

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/service/push"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/settings/schema"
)

var (
	ErrUnsupportedJobControlOperation = errors.New("unsupported job control operation")
	ErrJobNotFound                    = errors.New("job not found")
)

type Config struct {
	Name      config.JobScheduler
	EnvScript string
}

type Service struct {
	execSvc     execution.Executor
	settingsSvc *settings.Service
	logger      *slog.Logger
	cfg         Config
	ops         Scheduler
	publisher   push.Publisher

	// jobCache avoids hammering the external scheduler CLI on every GET /api/scheduler/jobs.
	// Key = canonical query (user + queue + node)，TTL 跟随 scheduler 域 JobCacheTTL。
	jobCache *cache.TTLCache[string, JobListResult]
}

// jobCacheMaxEntries 是作业列表缓存的条目硬顶。
// 与 connListCache 不同，本缓存的 key 由调用方查询参数（user|queue|node）直接
// 拼成，键空间由请求内容决定而非服务端推导，没有自然边界；此上限给内存占用
// 一个与 TTL 配置无关的封顶值，正常使用（活跃用户 × 少量过滤组合）远低于它。
const jobCacheMaxEntries = 4096

func NewService(execSvc execution.Executor, settingsSvc *settings.Service, cfg Config, logger *slog.Logger, publisher push.Publisher) (*Service, error) {
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
	ops := newDriver(cfg.Name, cfg.EnvScript)
	if ops == nil {
		return nil, fmt.Errorf("scheduler: unsupported scheduler %q", cfg.Name)
	}
	svc := &Service{
		execSvc:     execSvc,
		settingsSvc: settingsSvc,
		logger:      logger,
		cfg:         cfg,
		ops:         ops,
		publisher:   publisher,
		jobCache: cache.NewTTLCache[string, JobListResult](
			// 缓存级 TTL = 代码默认值，作为 settings 不可读/未配置时的唯一兜底来源
			// （调用方无需自备默认值，传 0 即走这里）。
			time.Duration(schema.SchedulerJobCacheTTLDefault) * time.Second).
			SetMaxEntries(jobCacheMaxEntries).
			EnableJanitor(),
	}
	return svc, nil
}
