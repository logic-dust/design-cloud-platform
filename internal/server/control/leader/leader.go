package leader

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"design-cloud-engine/internal/model"
)

const (
	LeaseTaskName  = "cluster_control_plane_leader"
	LeaseTTL       = 30 * time.Second
	RenewInterval  = 15 * time.Second
	StandbyMessage = "standby server"
)

type Reader interface {
	IsLeader() bool
	LeaderID() string
}

// TaskLeaseStore is the subset of db.TaskLeaseRepo that Elector needs.
// This interface enables testing with a mock store.
type TaskLeaseStore interface {
	TryAcquire(ctx context.Context, taskName, ownerID string, ttl time.Duration) (bool, error)
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
	store  TaskLeaseStore

	mu            sync.RWMutex
	isLeader      bool
	leaderID      string
	leaseDeadline time.Time

	cancel context.CancelFunc
	done   chan struct{}
}

func NewElector(nodeID string, logger *slog.Logger, store TaskLeaseStore) *Elector {
	return &Elector{
		nodeID: nodeID,
		logger: logger,
		store:  store,
		done:   make(chan struct{}),
	}
}

func (e *Elector) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	e.cancel = cancel

	e.refresh(ctx)

	go func() {
		ticker := time.NewTicker(RenewInterval)
		defer ticker.Stop()
		defer close(e.done)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.refresh(ctx)
			}
		}
	}()
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
	e.setState(false, "")
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

func (e *Elector) refresh(ctx context.Context) {
	// Get database time for consistency across nodes
	dbNow, err := e.store.GetDatabaseTime(ctx)
	if err != nil {
		e.logger.Warn("Failed to get database time, using local time", "error", err)
		dbNow = time.Now()
	}

	acquired, err := e.store.TryAcquire(ctx, LeaseTaskName, e.nodeID, LeaseTTL)
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
	if ok {
		leaderID = lease.OwnerID
	}

	if acquired {
		// Use database time for deadline calculation
		e.setLeader(dbNow.Add(LeaseTTL))
		return
	}

	e.mu.RLock()
	deadline := e.leaseDeadline
	currentlyLeader := e.isLeader
	e.mu.RUnlock()

	// Use database time to check if lease expired
	if currentlyLeader && !deadline.IsZero() && dbNow.After(deadline) {
		e.logger.Warn("Leader lease expired (checked against database time), demoting to standby")
		e.setState(false, leaderID)
		return
	}

	e.setFollower(leaderID)
}

func (e *Elector) handleRefreshFailure(dbNow time.Time) {
	e.mu.RLock()
	deadline := e.leaseDeadline
	currentlyLeader := e.isLeader
	leaderID := e.leaderID
	e.mu.RUnlock()

	// Use database time to check if lease expired
	if currentlyLeader && !deadline.IsZero() && dbNow.After(deadline) {
		e.logger.Warn("Leader lease refresh deadline exceeded (checked against database time), demoting to standby")
		e.setState(false, leaderID)
	}
}

func (e *Elector) setLeader(deadline time.Time) {
	e.mu.Lock()
	wasLeader := e.isLeader
	prevLeader := e.leaderID
	e.isLeader = true
	e.leaderID = e.nodeID
	e.leaseDeadline = deadline
	e.mu.Unlock()

	if !wasLeader {
		e.logger.Info("Acquired leader lease", "node_id", e.nodeID)
		return
	}
	if prevLeader != e.nodeID {
		e.logger.Info("Leader lease refreshed", "node_id", e.nodeID)
	}
}

func (e *Elector) setFollower(leaderID string) {
	e.setState(false, leaderID)
}

func (e *Elector) setState(isLeader bool, leaderID string) {
	e.mu.Lock()
	wasLeader := e.isLeader
	prevLeader := e.leaderID
	e.isLeader = isLeader
	e.leaderID = leaderID
	if !isLeader {
		e.leaseDeadline = time.Time{}
	}
	e.mu.Unlock()

	switch {
	case wasLeader && !isLeader:
		e.logger.Warn("Lost leader lease, becoming standby", "leader", leaderID)
	case !wasLeader && !isLeader && prevLeader != leaderID && leaderID != "":
		e.logger.Info("Observed current leader lease owner", "leader", leaderID)
	}
}
