package leader

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"design-cloud-engine/internal/model"
)

const (
	// LeaseTaskName is the task_leases row that elects which server owns Agent command connections.
	// It is a server-role lease, not a product "Engine" identity.
	// Changing this string opens a new row; the previous name expires on its own within LeaseTTL.
	LeaseTaskName = "cluster_server_control_leader"
	// LeaseTTL 与 RenewInterval 是默认选主参数；可由 WithLeaseTTL/WithRenewInterval 覆盖
	// （装配层从 cluster.lease_ttl / cluster.renew_interval 配置注入）。
	LeaseTTL       = 30 * time.Second
	RenewInterval  = 15 * time.Second
	StandbyMessage = "standby server"
)

type Reader interface {
	IsLeader() bool
	LeaderID() string
	// LocalEpoch：自己持约时的 epoch，仅 Leader 有意义（Standby 返回 0）；
	// ForwardDispatch 服务端用它校验请求携带的 leader_epoch。
	LocalEpoch() uint64
	// LeaderEpoch：最近一次 refresh 从租约行读到的当前 Leader epoch；
	// Standby 转发时填入请求。未观测到时为 0。
	LeaderEpoch() uint64
}

// TaskLeaseRepo is the subset of db.TaskLeaseRepo that Elector needs.
// This interface enables testing with a mock store.
type TaskLeaseRepo interface {
	TryAcquire(ctx context.Context, taskName, ownerID string, ttl time.Duration) (bool, uint64, error)
	GetDatabaseTime(ctx context.Context) (time.Time, error)
	Get(ctx context.Context, taskName string) (model.TaskLease, bool, error)
	ReleaseIfOwner(ctx context.Context, taskName, ownerID string) (bool, error)
}

// Elector keeps a local view of the current leader by acquiring and renewing
// a shared database lease. It is not a consensus algorithm; behavior is
// effectively "first to acquire wins, then renew until expiry".
type Elector struct {
	nodeID string
	logger *slog.Logger
	store  TaskLeaseRepo

	leaseTTL      time.Duration
	renewInterval time.Duration

	mu            sync.RWMutex
	isLeader      bool
	leaderID      string
	localEpoch    uint64
	leaderEpoch   uint64
	leaseDeadline time.Time
	demotionHook  func(newLeaderID string)

	cancel context.CancelFunc
	done   chan struct{}
}

// Option 覆盖 Elector 的选主参数；非正数值一律忽略并保留默认。
type Option func(*Elector)

// WithLeaseTTL 设置租约 TTL（失去租约后的故障接管窗口上界）。
func WithLeaseTTL(d time.Duration) Option {
	return func(e *Elector) {
		if d > 0 {
			e.leaseTTL = d
		}
	}
}

// WithRenewInterval 设置续约间隔；必须显著小于 leaseTTL（由配置校验保证）。
func WithRenewInterval(d time.Duration) Option {
	return func(e *Elector) {
		if d > 0 {
			e.renewInterval = d
		}
	}
}

func NewElector(nodeID string, logger *slog.Logger, store TaskLeaseRepo, opts ...Option) *Elector {
	e := &Elector{
		nodeID:        nodeID,
		logger:        logger,
		store:         store,
		leaseTTL:      LeaseTTL,
		renewInterval: RenewInterval,
		done:          make(chan struct{}),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// SetDemotionHook 注册降级回调，必须在 Start 之前调用（Start 会立即执行首次 refresh）。
// 回调在 Leader 失去租约时触发（含 Stop 主动释放），用于控制面主动断流自隔离。
func (e *Elector) SetDemotionHook(fn func(newLeaderID string)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.demotionHook = fn
}

func (e *Elector) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	e.cancel = cancel

	e.refresh(ctx)

	go func() {
		// ±20% 抖动：避免所有节点按同一节拍 TryAcquire 造成 DB 行锁争用。
		timer := time.NewTimer(e.jitteredRenewInterval())
		defer timer.Stop()
		defer close(e.done)

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				e.refresh(ctx)
				timer.Reset(e.jitteredRenewInterval())
			}
		}
	}()
}

// jitteredRenewInterval 返回 [0.8, 1.2) × renewInterval 的随机间隔。
func (e *Elector) jitteredRenewInterval() time.Duration {
	return e.renewInterval * time.Duration(80+rand.IntN(40)) / 100
}

func (e *Elector) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	if e.done != nil {
		<-e.done
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := e.store.ReleaseIfOwner(ctx, LeaseTaskName, e.nodeID); err != nil {
		e.logger.Warn("Failed to release leader lease", "error", err)
	}
	e.setState(false, "", 0)
}

func (e *Elector) IsLeader() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.isLeader
}

func (e *Elector) LeaderID() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.leaderID
}

func (e *Elector) LocalEpoch() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.localEpoch
}

func (e *Elector) LeaderEpoch() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.leaderEpoch
}

// RefreshNow 立即执行一次租约刷新；供转发重试路径在 Leader 信息失效时主动收敛。
func (e *Elector) RefreshNow(ctx context.Context) {
	e.refresh(ctx)
}

func (e *Elector) refresh(ctx context.Context) {
	// Get database time for consistency across nodes
	dbNow, err := e.store.GetDatabaseTime(ctx)
	if err != nil {
		e.logger.Warn("Failed to get database time, using local time", "error", err)
		dbNow = time.Now()
	}

	acquired, epoch, err := e.store.TryAcquire(ctx, LeaseTaskName, e.nodeID, e.leaseTTL)
	if err != nil {
		e.logger.Warn("Failed to refresh leader lease", "error", err)
		e.handleRefreshFailure(dbNow)
		return
	}

	lease, ok, err := e.store.Get(ctx, LeaseTaskName)
	if err != nil {
		e.logger.Warn("Failed to load leader lease state", "error", err)
		e.handleRefreshFailure(dbNow)
		return
	}

	leaderID := ""
	var observedEpoch uint64
	if ok {
		leaderID = lease.OwnerID
		observedEpoch = lease.Epoch
	}

	if acquired {
		// Use database time for deadline calculation
		e.setLeader(dbNow.Add(e.leaseTTL), epoch)
		return
	}

	e.mu.RLock()
	deadline := e.leaseDeadline
	currentlyLeader := e.isLeader
	e.mu.RUnlock()

	// Use database time to check if lease expired
	if currentlyLeader && !deadline.IsZero() && dbNow.After(deadline) {
		e.logger.Warn("Leader lease expired (checked against database time), demoting to standby")
		e.setState(false, leaderID, observedEpoch)
		return
	}

	e.setFollower(leaderID, observedEpoch)
}

func (e *Elector) handleRefreshFailure(dbNow time.Time) {
	e.mu.RLock()
	deadline := e.leaseDeadline
	currentlyLeader := e.isLeader
	leaderID := e.leaderID
	leaderEpoch := e.leaderEpoch
	e.mu.RUnlock()

	// Use database time to check if lease expired
	if currentlyLeader && !deadline.IsZero() && dbNow.After(deadline) {
		e.logger.Warn("Leader lease refresh deadline exceeded (checked against database time), demoting to standby")
		e.setState(false, leaderID, leaderEpoch)
	}
}

func (e *Elector) setLeader(deadline time.Time, epoch uint64) {
	e.mu.Lock()
	wasLeader := e.isLeader
	prevLeader := e.leaderID
	prevEpoch := e.localEpoch
	e.isLeader = true
	e.leaderID = e.nodeID
	e.localEpoch = epoch
	e.leaderEpoch = epoch
	e.leaseDeadline = deadline
	e.mu.Unlock()

	if !wasLeader {
		e.logger.Info("Acquired leader lease", "node_id", e.nodeID, "epoch", epoch)
		return
	}
	if prevLeader != e.nodeID {
		e.logger.Info("Leader lease refreshed", "node_id", e.nodeID, "epoch", epoch)
		return
	}
	if prevEpoch != epoch {
		e.logger.Info("Leader epoch advanced after re-acquire", "node_id", e.nodeID, "epoch", epoch)
	}
}

func (e *Elector) setFollower(leaderID string, leaderEpoch uint64) {
	e.setState(false, leaderID, leaderEpoch)
}

func (e *Elector) setState(isLeader bool, leaderID string, leaderEpoch uint64) {
	e.mu.Lock()
	wasLeader := e.isLeader
	prevLeader := e.leaderID
	// 降级时若 DB 不可用，保留的旧 leaderID 会指向本节点。自我引用会让降级钩子与
	// acceptAgent 把 Agent 重定向回同一个 Standby，形成被拒重连循环，因此必须清空。
	if !isLeader && leaderID == e.nodeID {
		leaderID = ""
	}
	e.isLeader = isLeader
	e.leaderID = leaderID
	e.leaderEpoch = leaderEpoch
	if !isLeader {
		e.leaseDeadline = time.Time{}
		e.localEpoch = 0
	}
	hook := e.demotionHook
	e.mu.Unlock()

	switch {
	case wasLeader && !isLeader:
		e.logger.Warn("Lost leader lease, becoming standby", "leader", leaderID)
		if hook != nil {
			hook(leaderID)
		}
	case !wasLeader && !isLeader && prevLeader != leaderID && leaderID != "":
		e.logger.Info("Observed current leader lease owner", "leader", leaderID)
	}
}
