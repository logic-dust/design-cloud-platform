// Package overview 是概览页聚合读模型：面向 portal 概览端点的跨域只读聚合
// （nodes / node_runtime / scheduler / settings），无写路径。属于 L1 查询聚合，
// 非业务命令域。
package overview

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/concurrency"
	"design-cloud-engine/internal/platform/format"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/service/scheduler"
)

// nodeStatusData 节点注册视角统计（nodes 表，低频变化）。
type nodeStatusData struct {
	Nodes NodeStats
}

// runtimeData 节点资源聚合（node_runtime 表，跟随 agent 上报节律）。
type runtimeData struct {
	CPU    CPUStats
	GPU    GPUStats
	Load   LoadStats
	Memory MemoryStats
}

type Service struct {
	logger       *slog.Logger
	nodeRepo     *db.NodeRepo
	storageRepo  *db.StorageRepo
	settingsSvc  SettingsReader
	schedulerSvc *scheduler.Service

	// Independent component caches（统一原语 cache.Cell：TTL 过期 + 采样指纹）
	nodeStatusCache cache.Cell[nodeStatusData]
	runtimeCache    cache.Cell[runtimeData]
	schedulerCache  cache.Cell[SchedulerStats]
	storageCache    cache.Cell[StorageStats]

	// SSE 订阅者：刷新完成通知通道集合。第一个订阅者启动后台刷新循环，
	// 最后一个退订停止循环（无人订阅不刷新，零开销）。
	subMu         sync.Mutex
	subscribers   map[chan struct{}]struct{}
	refreshCancel context.CancelFunc
	refreshDone   chan struct{}
}

func NewService(
	logger *slog.Logger,
	nodeRepo *db.NodeRepo,
	storageRepo *db.StorageRepo,
	settingsSvc SettingsReader,
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

func (s *Service) GetOverview(ctx context.Context, jobsMode JobsStatsMode, sessionUser string) (Response, error) {
	now := time.Now()
	resp := Response{
		SampledAt: now,
	}

	// 1. Fetch Platform Name
	platSettings, err := s.settingsSvc.GetPlatformSettings(ctx)
	if err == nil {
		resp.Name = platSettings.Name
	}

	// 2a. Refresh & Assemble Node Status (TTL: 低频, nodes 表注册视角)
	statusData := s.nodeStatusCache.GetOrRefresh(ctx, s.nodeStatusTTL(ctx), s.refreshNodeStatus)
	resp.Nodes = statusData.Nodes
	resp.NodeSampledAt = s.nodeStatusCache.SampledAt()

	// 2b. Refresh & Assemble Metrics Stats (TTL: agent 资源指标上报间隔，默认 60s)
	resData := s.runtimeCache.GetOrRefresh(ctx, s.runtimeTTL(ctx), s.refreshNodeRuntime)
	resp.CPU = resData.CPU
	resp.GPU = resData.GPU
	resp.Memory = resData.Memory
	resp.Load = resData.Load
	resp.MetricsSampledAt = s.runtimeCache.SampledAt()

	// 3. Refresh & Assemble Scheduler (Hosts, Jobs, Queues) (TTL: 10m 默认，可配)
	schedCached := s.schedulerCache.GetOrRefresh(ctx, s.schedulerCacheTTL(ctx), s.refreshScheduler)
	resp.Scheduler = schedCached
	jobs, err := s.schedulerJobsForMode(ctx, schedCached.Jobs, jobsMode, sessionUser)
	if err != nil {
		return Response{}, err
	}
	resp.Scheduler.Jobs = jobs
	resp.SchedulerSampledAt = s.schedulerCache.SampledAt()

	// 4. Refresh & Assemble Storage (TTL: 30m 默认，可配)
	resp.Storage = s.storageCache.GetOrRefresh(ctx, s.storageCacheTTL(ctx), s.refreshStorage)
	resp.StorageSampledAt = s.storageCache.SampledAt()

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
		resp.Alert = AlertView{
			StorageUsedPercent: storagePct,
			LoadRatio:          loadRatio,
		}
	}

	return resp, nil
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
		var done chan struct{}
		if len(s.subscribers) == 0 {
			done = s.stopRefreshLoop()
		}
		s.subMu.Unlock()
		if done != nil {
			<-done
		}
	}
}

// startRefreshLoop 启动后台刷新循环：按最短 TTL 节律同步刷新各块缓存，
// 每次刷新完成后通知所有订阅者。仅在有订阅者时运行。
func (s *Service) startRefreshLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	s.refreshCancel = cancel
	done := make(chan struct{})
	s.refreshDone = done

	go func() {
		defer close(done)
		defer cancel()
		ticker := time.NewTicker(s.loopInterval(ctx))
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer concurrency.SafeRecover(s.logger, "overview-refresh-tick")()
					s.refreshAllSync(ctx)
					s.notifySubscribers()
				}()
			}
		}
	}()
}

func (s *Service) stopRefreshLoop() chan struct{} {
	done := s.refreshDone
	if s.refreshCancel != nil {
		s.refreshCancel()
		s.refreshCancel = nil
	}
	s.refreshDone = nil
	return done
}

// loopInterval 取各缓存块 TTL 的最小值作为检查节律：
// 每个 TTL 周期内至少检查一次，保证任何一块过期后尽快刷新。
// runtimeTTL 可能配到 3600s，此时用 scheduler 的 TTL 兜底。
func (s *Service) loopInterval(ctx context.Context) time.Duration {
	return loopMinTTL(
		min(s.nodeStatusTTL(ctx), s.runtimeTTL(ctx)),
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
	if !s.runtimeCache.IsFresh(s.runtimeTTL(ctx)) {
		s.refreshNodeRuntime(ctx)
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

// runtimeTTL 返回节点资源聚合缓存的刷新周期：overview 域独立配置
// （默认 60s，可配 30-3600s）。大屏无需跟随 agent 实时刷新，但也不应比
// 数据源更勤——下限保护为 agent 上报间隔 + metricsSlack（见该常量
// 注释：相等 TTL 存在相位陷阱，裕量保证查询时刻最新帧已落库）。
func (s *Service) runtimeTTL(ctx context.Context) time.Duration {
	secs := overviewRefreshSecs(ctx, s.settingsSvc.GetOverviewSettings,
		func(d schema.OverviewSettings) int { return d.MetricsRefreshInterval },
		schema.OverviewMetricsRefreshIntervalDefault)
	ttl := time.Duration(secs) * time.Second

	// 下限 = agent 上报间隔 + 5s 裕量：保证查询时刻最新帧已落库。
	if agentSettings, err := s.settingsSvc.GetAgentSettings(ctx); err == nil {
		if report := agentSettings.Monitor.MetricsInterval; report > 0 {
			if minTTL := time.Duration(report)*time.Second + metricsSlack; ttl < minTTL {
				ttl = minTTL
			}
		}
	}
	return ttl
}

// metricsSlack 是资源缓存相对 agent 上报间隔的裕量（5s）。
//
// 为什么需要裕量：若 TTL 与 agent 上报间隔相等，存在相位陷阱——
// agent 上报是"定时触发 + 网络传输 + 写库"链路，有抖动；overview 的
// 刷新循环（loop tick）与上报相位不可能对齐。TTL==上报间隔时，
// 查询时刻可能"刚好错过"最新帧（拿到的还是上一帧），等于周期性白查。
// 裕量保证查询时刻最新帧必然已落库：TTL = 上报间隔 + 5s 时，
// 每次查询至少晚于最新帧落库 5s，查询恒有效。
//
// 为什么 5s：上报链路抖动常规 <1s（本地网络 + 单次写库），5s 留足
// 余量又不至于让大屏数据明显滞后（比上报间隔多 5s 感知不到）。
// 若未来上报链路变慢（跨地域部署、写库排队），此常量应相应调大。
const metricsSlack = 5 * time.Second

// nodeStatusTTL 返回节点状态统计缓存的刷新周期：nodes 表是注册视角，
// 节点数量低频变化（注册/删除），无需跟随资源指标上报节律高频刷新。
// 默认 5 分钟（overview 域可配），保护下限 1 分钟。
func (s *Service) nodeStatusTTL(ctx context.Context) time.Duration {
	secs := overviewRefreshSecs(ctx, s.settingsSvc.GetOverviewSettings,
		func(d schema.OverviewSettings) int { return d.NodeStatusRefreshInterval },
		schema.OverviewNodeStatusRefreshIntervalDefault)
	ttl := max(time.Duration(secs)*time.Second, time.Minute)
	return ttl
}

// schedulerCacheTTL 返回调度器缓存刷新周期：跟随 overview 域 schedulerRefreshInterval 配置
// （默认 600s，可配 90-3600s；下限 90s > scheduler JobCacheTTL 默认 60，
// 保证下游慢于上游）。0/未配置回退默认。
func (s *Service) schedulerCacheTTL(ctx context.Context) time.Duration {
	return time.Duration(overviewRefreshSecs(ctx, s.settingsSvc.GetOverviewSettings,
		func(d schema.OverviewSettings) int { return d.SchedulerRefreshInterval },
		schema.OverviewSchedulerRefreshIntervalDefault)) * time.Second
}

// storageCacheTTL 返回存储缓存刷新周期：跟随 overview 域 storageRefreshInterval 配置
// （默认 1800s，可配 60-3600s）。0/未配置回退默认。
func (s *Service) storageCacheTTL(ctx context.Context) time.Duration {
	return time.Duration(overviewRefreshSecs(ctx, s.settingsSvc.GetOverviewSettings,
		func(d schema.OverviewSettings) int { return d.StorageRefreshInterval },
		schema.OverviewStorageRefreshIntervalDefault)) * time.Second
}

// overviewRefreshSecs 读取 overview 块刷新间隔设置：0/读取失败回退默认值。
func overviewRefreshSecs[T any](ctx context.Context,
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

// refreshNodeStatus 刷新已接入节点运行状态（online/offline/maintenance）。
func (s *Service) refreshNodeStatus(ctx context.Context) {
	now := time.Now()
	var nodes NodeStats

	online, offline, maintenance, err := s.nodeRepo.CountRuntimeByStatus(ctx)
	if err == nil {
		nodes.Online = online
		nodes.Offline = offline
		nodes.Maintenance = maintenance
		nodes.Configured = online + offline + maintenance
	} else {
		s.logger.Warn("Failed to fetch node status count", "error", err)
	}

	s.nodeStatusCache.Set(nodeStatusData{Nodes: nodes}, now)
}

// metricsStaleFloor 是资源新鲜度窗口的下限：等于控制面 node_runtime 落盘节流间隔
// （contract.DefaultMetricsFlushInterval=180s）的两倍。节流后侧表最新帧最多滞后一个
// 落盘周期，窗口必须覆盖两个周期，否则 online_* 会被误判为过期。
const metricsStaleFloor = 6 * time.Minute

// metricsStaleAfter 返回资源聚合判定指标新鲜的时间窗：至少覆盖两个落盘周期与
// 两个 Agent 上报周期，避免合法的长间隔配置把 online_* 全部判成过期。
func (s *Service) metricsStaleAfter(ctx context.Context) time.Duration {
	stale := metricsStaleFloor
	if agentSettings, err := s.settingsSvc.GetAgentSettings(ctx); err == nil {
		if interval := agentSettings.Monitor.MetricsInterval; interval > 0 {
			stale = max(stale, 2*time.Duration(interval)*time.Second)
		}
	}
	return stale
}

// refreshNodeRuntime 刷新节点资源聚合（node_runtime 表，跟随上报节律）。
func (s *Service) refreshNodeRuntime(ctx context.Context) {
	now := time.Now()
	var cpu CPUStats
	var gpu GPUStats
	var memory MemoryStats
	var load LoadStats

	staleBefore := now.Add(-s.metricsStaleAfter(ctx))
	stats, err := s.nodeRepo.AggregateRuntime(ctx, staleBefore)
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

	s.runtimeCache.Set(runtimeData{
		CPU:    cpu,
		GPU:    gpu,
		Memory: memory,
		Load:   load,
	}, now)
}

func (s *Service) refreshScheduler(ctx context.Context) {
	now := time.Now()
	var global JobStats
	jobCounts, err := s.schedulerSvc.JobStatusCounts(ctx, scheduler.JobListQuery{})
	if err == nil {
		global = jobStatsFromScheduler(jobCounts)
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
		Hosts:  schedSummary,
		Jobs:   global,
		Queues: queueResult.Queues,
	}, now)
}

func (s *Service) refreshStorage(ctx context.Context) {
	now := time.Now()

	storageSettings, err := s.settingsSvc.GetStorageSettings(ctx)
	if err != nil {
		s.logger.Warn("Failed to fetch storage settings for overview", "error", err)
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
	if dashSettings, err := s.settingsSvc.GetOverviewSettings(ctx); err == nil && dashSettings.StorageQueryWindow > 0 {
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
