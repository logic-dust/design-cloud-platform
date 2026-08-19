package dashboard

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/common/format"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/service/scheduler"
	"design-cloud-engine/internal/server/service/settings/schema"
)

// nodeStatusData 节点注册视角统计（nodes 表，低频变化）。
type nodeStatusData struct {
	Nodes NodeStats
}

// nodeResourceData 节点资源聚合（resource_reports 表，跟随 agent 上报节律）。
type nodeResourceData struct {
	CPU    CPUStats
	GPU    GPUStats
	Load   LoadStats
	Memory MemoryStats
}

// settingsProvider 是 dashboard 依赖的 settings 能力子集（便于测试注入 stub）。
type settingsProvider interface {
	GetAgentSettings(ctx context.Context) (schema.AgentSettings, error)
	GetStorageSettings(ctx context.Context) (schema.StorageSettings, error)
	GetPlatformSettings(ctx context.Context) (schema.PlatformSettings, error)
	GetDashboardSettings(ctx context.Context) (schema.DashboardSettings, error)
	GetAlertSettings(ctx context.Context) (schema.AlertSettings, error)
}

type Service struct {
	logger       *slog.Logger
	nodeRepo     *db.NodeRepo
	storageRepo  *db.StorageRepo
	settingsSvc  settingsProvider
	schedulerSvc *scheduler.Service

	// Independent component caches（统一原语 cache.Cell：TTL 过期 + 采样指纹）
	nodeStatusCache   cache.Cell[nodeStatusData]
	nodeResourceCache cache.Cell[nodeResourceData]
	schedulerCache    cache.Cell[SchedulerStats]
	storageCache      cache.Cell[StorageStats]

	// SSE 订阅者：刷新完成通知通道集合。第一个订阅者启动后台刷新循环，
	// 最后一个退订停止循环（无人订阅不刷新，零开销）。
	subMu         sync.Mutex
	subscribers   map[chan struct{}]struct{}
	refreshCancel context.CancelFunc
}

func NewService(
	logger *slog.Logger,
	nodeRepo *db.NodeRepo,
	storageRepo *db.StorageRepo,
	settingsSvc settingsProvider,
	schedulerSvc *scheduler.Service,
) *Service {
	return &Service{
		logger:       logger,
		nodeRepo:     nodeRepo,
		storageRepo:  storageRepo,
		settingsSvc:  settingsSvc,
		schedulerSvc: schedulerSvc,
		subscribers:  make(map[chan struct{}]struct{}),
	}
}

func (s *Service) GetDashboard(ctx context.Context) (Response, error) {
	now := time.Now()
	dash := Response{
		SampledAt: now,
	}

	// 1. Fetch Platform Name
	platSettings, err := s.settingsSvc.GetPlatformSettings(ctx)
	if err == nil {
		dash.Name = platSettings.Name
	}

	// 2a. Refresh & Assemble Node Status (TTL: 低频, nodes 表注册视角)
	statusData := s.nodeStatusCache.GetOrRefresh(ctx, s.nodeStatusTTL(ctx), s.refreshNodeStatus)
	dash.Nodes = statusData.Nodes
	dash.NodeSampledAt = s.nodeStatusCache.SampledAt()

	// 2b. Refresh & Assemble Resource Stats (TTL: agent 资源上报间隔，默认 60s)
	resData := s.nodeResourceCache.GetOrRefresh(ctx, s.nodeResourceTTL(ctx), s.refreshNodeResources)
	dash.CPU = resData.CPU
	dash.GPU = resData.GPU
	dash.Memory = resData.Memory
	dash.Load = resData.Load
	dash.ResourceSampledAt = s.nodeResourceCache.SampledAt()

	// 3. Refresh & Assemble Scheduler (Hosts, Jobs, Queues) (TTL: 10m 默认，可配)
	dash.Scheduler = s.schedulerCache.GetOrRefresh(ctx, s.schedulerCacheTTL(ctx), s.refreshScheduler)
	dash.SchedulerSampledAt = s.schedulerCache.SampledAt()

	// 4. Refresh & Assemble Storage (TTL: 30m 默认，可配)
	dash.Storage = s.storageCache.GetOrRefresh(ctx, s.storageCacheTTL(ctx), s.refreshStorage)
	dash.StorageSampledAt = s.storageCache.SampledAt()

	// 5. Alert visual thresholds（前端视觉阈值来自 DomainAlert 配置，避免硬编码）
	if alertSettings, err := s.settingsSvc.GetAlertSettings(ctx); err == nil {
		var storagePct float64
		if alertSettings.SharedStorage.Enabled {
			storagePct = alertSettings.SharedStorage.MaxUsedPercent
		}
		var loadRatio float64
		if alertSettings.Load.Enabled {
			loadRatio = alertSettings.Load.MaxLoadPercent / 100.0
		}
		dash.Alert = AlertView{
			StorageUsedPercent: storagePct,
			LoadRatio:          loadRatio,
		}
	}

	return dash, nil
}

// Subscribe 注册一个 SSE 订阅者，返回通知 channel 和取消函数。
// 第一个订阅者启动后台刷新循环；最后一个退订停止循环。
// 通知语义：刷新完成（有数据变化可能）时向所有订阅者发一次信号。
// channel 缓冲 1，消费慢时丢弃（SSE 客户端不追帧）。
func (s *Service) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)

	s.subMu.Lock()
	if len(s.subscribers) == 0 {
		s.startRefreshLoop()
	}
	s.subscribers[ch] = struct{}{}
	s.subMu.Unlock()

	return ch, func() {
		s.subMu.Lock()
		delete(s.subscribers, ch)
		if len(s.subscribers) == 0 {
			s.stopRefreshLoop()
		}
		s.subMu.Unlock()
	}
}

// startRefreshLoop 启动后台刷新循环：按最短 TTL 节律同步刷新各块缓存，
// 每次刷新完成后通知所有订阅者。仅在有订阅者时运行。
func (s *Service) startRefreshLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	s.refreshCancel = cancel

	go func() {
		defer cancel()
		ticker := time.NewTicker(s.loopInterval(ctx))
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer common.SafeRecover(s.logger, "dashboard-refresh-tick")()
					s.refreshAllSync(ctx)
					s.notifySubscribers()
				}()
			}
		}
	}()
}

func (s *Service) stopRefreshLoop() {
	if s.refreshCancel != nil {
		s.refreshCancel()
		s.refreshCancel = nil
	}
}

// loopInterval 取各缓存块 TTL 的最小值作为检查节律：
// 每个 TTL 周期内至少检查一次，保证任何一块过期后尽快刷新。
// nodeResourceTTL 可能配到 3600s，此时用 scheduler 的 TTL 兜底。
func (s *Service) loopInterval(ctx context.Context) time.Duration {
	return loopMinTTL(
		min(s.nodeStatusTTL(ctx), s.nodeResourceTTL(ctx)),
		s.schedulerCacheTTL(ctx),
		s.storageCacheTTL(ctx),
	)
}

// loopMinTTL 取多块 TTL 的最小值，并保护下限 15s。
func loopMinTTL(ttls ...time.Duration) time.Duration {
	interval := ttls[0]
	for _, ttl := range ttls[1:] {
		if ttl < interval {
			interval = ttl
		}
	}
	if interval < 15*time.Second {
		interval = 15 * time.Second
	}
	return interval
}

// refreshAllSync 同步刷新所有过期缓存块（与 GetOrRefresh 相同的 TTL 判断，
// 但同步执行，保证返回后数据已更新——异步版本会丢帧）。
func (s *Service) refreshAllSync(ctx context.Context) {
	if !s.nodeStatusCache.IsFresh(s.nodeStatusTTL(ctx)) {
		s.refreshNodeStatus(ctx)
	}
	if !s.nodeResourceCache.IsFresh(s.nodeResourceTTL(ctx)) {
		s.refreshNodeResources(ctx)
	}
	if !s.schedulerCache.IsFresh(s.schedulerCacheTTL(ctx)) {
		s.refreshScheduler(ctx)
	}
	if !s.storageCache.IsFresh(s.storageCacheTTL(ctx)) {
		s.refreshStorage(ctx)
	}
}

// notifySubscribers 向所有订阅者广播刷新完成信号，非阻塞（慢消费者丢信号）。
func (s *Service) notifySubscribers() {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// nodeResourceTTL 返回节点资源聚合缓存的刷新周期：dashboard 域独立配置
// （默认 60s，可配 30-3600s）。大屏无需跟随 agent 实时刷新，但也不应比
// 数据源更勤——下限保护为 agent 上报间隔 + resourceReportSlack（见该常量
// 注释：相等 TTL 存在相位陷阱，裕量保证查询时刻最新帧已落库）。
func (s *Service) nodeResourceTTL(ctx context.Context) time.Duration {
	secs := dashboardRefreshSecs(ctx, s.settingsSvc.GetDashboardSettings,
		func(d schema.DashboardSettings) int { return d.ResourceRefreshInterval },
		schema.DashboardResourceRefreshIntervalDefault)
	ttl := time.Duration(secs) * time.Second

	// 下限 = agent 上报间隔 + 5s 裕量：保证查询时刻最新帧已落库。
	if agentSettings, err := s.settingsSvc.GetAgentSettings(ctx); err == nil {
		if report := agentSettings.Monitor.ResourceReportInterval; report > 0 {
			if minTTL := time.Duration(report)*time.Second + resourceReportSlack; ttl < minTTL {
				ttl = minTTL
			}
		}
	}
	return ttl
}

// resourceReportSlack 是资源缓存相对 agent 上报间隔的裕量（5s）。
//
// 为什么需要裕量：若 TTL 与 agent 上报间隔相等，存在相位陷阱——
// agent 上报是"定时触发 + 网络传输 + 写库"链路，有抖动；dashboard 的
// 刷新循环（loop tick）与上报相位不可能对齐。TTL==上报间隔时，
// 查询时刻可能"刚好错过"最新帧（拿到的还是上一帧），等于周期性白查。
// 裕量保证查询时刻最新帧必然已落库：TTL = 上报间隔 + 5s 时，
// 每次查询至少晚于最新帧落库 5s，查询恒有效。
//
// 为什么 5s：上报链路抖动常规 <1s（本地网络 + 单次写库），5s 留足
// 余量又不至于让大屏数据明显滞后（比上报间隔多 5s 感知不到）。
// 若未来上报链路变慢（跨地域部署、写库排队），此常量应相应调大。
const resourceReportSlack = 5 * time.Second

// nodeStatusTTL 返回节点状态统计缓存的刷新周期：nodes 表是注册视角，
// 节点数量低频变化（注册/删除），无需跟随资源上报节律高频刷新。
// 默认 5 分钟（dashboard 域可配），保护下限 1 分钟。
func (s *Service) nodeStatusTTL(ctx context.Context) time.Duration {
	secs := dashboardRefreshSecs(ctx, s.settingsSvc.GetDashboardSettings,
		func(d schema.DashboardSettings) int { return d.NodeStatusRefreshInterval },
		schema.DashboardNodeStatusRefreshIntervalDefault)
	ttl := max(time.Duration(secs)*time.Second, time.Minute)
	return ttl
}

// schedulerCacheTTL 返回调度器缓存刷新周期：跟随 dashboard 域 schedulerRefreshInterval 配置
// （默认 600s，可配 90-3600s；下限 90s > scheduler JobCacheTTL 默认 60，
// 保证下游慢于上游）。0/未配置回退默认。
func (s *Service) schedulerCacheTTL(ctx context.Context) time.Duration {
	return time.Duration(dashboardRefreshSecs(ctx, s.settingsSvc.GetDashboardSettings,
		func(d schema.DashboardSettings) int { return d.SchedulerRefreshInterval },
		schema.DashboardSchedulerRefreshIntervalDefault)) * time.Second
}

// storageCacheTTL 返回存储缓存刷新周期：跟随 dashboard 域 storageRefreshInterval 配置
// （默认 1800s，可配 60-3600s）。0/未配置回退默认。
func (s *Service) storageCacheTTL(ctx context.Context) time.Duration {
	return time.Duration(dashboardRefreshSecs(ctx, s.settingsSvc.GetDashboardSettings,
		func(d schema.DashboardSettings) int { return d.StorageRefreshInterval },
		schema.DashboardStorageRefreshIntervalDefault)) * time.Second
}

// dashboardRefreshSecs 读取 dashboard 块刷新间隔设置：0/读取失败回退默认值。
func dashboardRefreshSecs[T any](ctx context.Context,
	get func(context.Context) (T, error),
	pick func(T) int,
	def int,
) int {
	settings, err := get(ctx)
	if err != nil {
		return def
	}
	if secs := pick(settings); secs > 0 {
		return secs
	}
	return def
}

// refreshNodeStatus 刷新节点状态统计（nodes 表注册视角，低频）。
// 注意口径：Offline = 非 Online 的全部状态（pending/maintenance/disabled 均计
// offline）——大屏环形图 X/Y 语义（在线/总量），与 system.nodesStatus 的
// 显式 offline 口径不同，勿合并两处实现。
func (s *Service) refreshNodeStatus(ctx context.Context) {
	now := time.Now()
	var nodes NodeStats

	statusCounts, err := s.nodeRepo.CountByStatus(ctx)
	if err == nil {
		for status, count := range statusCounts {
			nodes.Configured += count
			if status == model.NodeStatusOnline {
				nodes.Online += count
			}
		}
	} else {
		s.logger.Warn("Failed to fetch node status count", "error", err)
	}

	s.nodeStatusCache.Set(nodeStatusData{Nodes: nodes}, now)
}

// refreshNodeResources 刷新节点资源聚合（resource_reports 表，跟随上报节律）。
func (s *Service) refreshNodeResources(ctx context.Context) {
	now := time.Now()
	var cpu CPUStats
	var gpu GPUStats
	var memory MemoryStats
	var load LoadStats

	staleBefore := now.Add(-2 * time.Minute)
	stats, err := s.nodeRepo.AggregateResourceStats(ctx, staleBefore)
	if err == nil {
		cpu.Configured = stats.TotalCores
		cpu.Online = stats.OnlineCores
		if stats.OnlineCores > 0 {
			cpu.Utilization = format.RoundTo(stats.SumCPUUtil/float64(stats.OnlineCores), 4)
			load.Load1m = format.RoundTo(stats.SumLoad1, 2)
			load.Load5m = format.RoundTo(stats.SumLoad5, 2)
			load.Load15m = format.RoundTo(stats.SumLoad15, 2)
		}

		gpu.Nodes = GPUNodesSubStats{
			Configured: stats.TotalGPUNodes,
			Online:     stats.OnlineGPUNodes,
		}
		gpu.Cards = GPUCardsSubStats{
			Configured: stats.TotalGPUs,
			Online:     stats.OnlineGPUs,
		}
		if stats.OnlineGPUs > 0 {
			gpu.Cards.Utilization = format.RoundTo(stats.SumGPUUtil/float64(stats.OnlineGPUs), 4)
		}

		if stats.ConfiguredGPUMem > 0 || stats.OnlineGPUMem > 0 {
			totalVal, totalUnit := format.AutoScaleKB(stats.ConfiguredGPUMem)
			onlineVal := format.ScaleKBTo(stats.OnlineGPUMem, totalUnit)
			usedVal := format.ScaleKBTo(stats.TotalGPUUsed, totalUnit)
			gpu.VRAM = GPUVRAMSubStats{
				Configured: totalVal,
				Online:     onlineVal,
				Used:       usedVal,
				Unit:       totalUnit,
			}
		} else {
			gpu.VRAM.Unit = "GB"
		}

		memTotalVal, memTotalUnit := format.AutoScaleKB(stats.ConfiguredMem)
		memOnlineVal := format.ScaleKBTo(stats.OnlineMem, memTotalUnit)
		memUsedVal := format.ScaleKBTo(stats.TotalMemUsed, memTotalUnit)
		memory.Configured = memTotalVal
		memory.Online = memOnlineVal
		memory.Used = memUsedVal
		memory.Unit = memTotalUnit
	} else {
		s.logger.Warn("Failed to aggregate resource stats", "error", err)
	}

	s.nodeResourceCache.Set(nodeResourceData{
		CPU:    cpu,
		GPU:    gpu,
		Memory: memory,
		Load:   load,
	}, now)
}

func (s *Service) refreshScheduler(ctx context.Context) {
	now := time.Now()
	var running, pending, suspended, completed, failed int

	listResult, err := s.schedulerSvc.ListJobs(ctx, scheduler.JobListQuery{})
	if err == nil {
		for _, job := range listResult.Jobs {
			switch s.schedulerSvc.NormalizeJobStatus(job) {
			case scheduler.JobStatusRunning:
				running++
			case scheduler.JobStatusPending:
				pending++
			case scheduler.JobStatusSuspended:
				suspended++
			case scheduler.JobStatusCompleted:
				completed++
			case scheduler.JobStatusFailed:
				failed++
			}
		}
	} else {
		s.logger.Warn("Failed to fetch jobs from scheduler", "error", err)
	}

	queueResult, err := s.schedulerSvc.ListQueues(ctx)
	if err != nil {
		s.logger.Warn("Failed to fetch queues from scheduler", "error", err)
	}

	var schedSummary SchedulerHostSummary
	hostResult, err := s.schedulerSvc.ListHosts(ctx)
	if err == nil {
		schedSummary.Total = hostResult.Total
		schedSummary.Online = hostResult.Online
	} else {
		s.logger.Warn("Failed to fetch compute hosts from scheduler", "error", err)
	}

	s.schedulerCache.Set(SchedulerStats{
		Hosts: schedSummary,
		Jobs: JobStats{
			Running:   running,
			Pending:   pending,
			Suspended: suspended,
			Completed: completed,
			Failed:    failed,
		},
		Queues: queueResult.Queues,
	}, now)
}

func (s *Service) refreshStorage(ctx context.Context) {
	now := time.Now()

	storageSettings, err := s.settingsSvc.GetStorageSettings(ctx)
	if err != nil {
		s.logger.Warn("Failed to fetch storage settings for dashboard", "error", err)
		return
	}

	// 无任何启用存储：清空缓存表达"未启用存储监控"（Items 为空即无存储），
	// 而非 0 容量的误导状态；sampledAt 记录本次确认时间，避免零值导致反复阻塞刷新。
	enabledStorages := storageSettings.EnabledStorages()
	if len(enabledStorages) == 0 {
		s.storageCache.Set(StorageStats{}, now)
		return
	}

	var items []SharedStorageStatus
	var totalKB, usedKB int64

	// 使用配置中的 StorageQueryWindow，默认为 7 天
	queryWindow := 7 * 24 * 60 * 60
	if dashSettings, err := s.settingsSvc.GetDashboardSettings(ctx); err == nil && dashSettings.StorageQueryWindow > 0 {
		queryWindow = dashSettings.StorageQueryWindow
	}

	storageReports, err := s.storageRepo.QueryLatestStorageReports(ctx, queryWindow)
	if err != nil {
		s.logger.Warn("Failed to fetch shared storage reports", "error", err)
	}

	reportMap := make(map[string]model.StorageReport)
	for _, r := range storageReports {
		reportMap[r.StorageID] = r
	}

	for _, cfg := range enabledStorages {
		storageStatus := SharedStorageStatus{
			ID:        cfg.ID,
			Name:      cfg.Name,
			Type:      cfg.Type,
			SampledAt: now,
		}
		if report, ok := reportMap[cfg.ID]; ok {
			totalVal, totalUnit := format.AutoScaleKB(report.Total)
			usedVal := format.ScaleKBTo(report.Used, totalUnit)

			storageStatus.Total = totalVal
			storageStatus.Used = usedVal
			storageStatus.Unit = totalUnit
			storageStatus.SampledAt = report.ReportedAt

			totalKB += report.Total
			usedKB += report.Used
		} else {
			storageStatus.Unit = "TB"
		}
		items = append(items, storageStatus)
	}

	var summary StorageSummary
	if totalKB > 0 {
		sumTotalVal, sumUnit := format.AutoScaleKB(totalKB)
		sumUsedVal := format.ScaleKBTo(usedKB, sumUnit)
		summary.Total = sumTotalVal
		summary.Used = sumUsedVal
		summary.Unit = sumUnit
		summary.Utilization = format.RoundTo(float64(usedKB)/float64(totalKB), 4)
	} else {
		summary.Unit = "TB"
	}

	s.storageCache.Set(StorageStats{
		Summary: summary,
		Items:   items,
	}, now)
}
