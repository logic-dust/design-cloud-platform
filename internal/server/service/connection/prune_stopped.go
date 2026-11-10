package connection

import (
	"context"
	"time"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/platform/concurrency"
)

const (
	stoppedPruneLeaseName = "connection_stopped_prune"
	// Retention is 90 days; scanning once a day is enough (was hourly).
	stoppedPruneInterval = 24 * time.Hour
	stoppedPruneTimeout  = 2 * time.Minute
)

func (s *Service) pruneOldStoppedConnections(ctx context.Context) (int64, error) {
	if s.connectionRepo == nil {
		return 0, nil
	}
	cutoff := time.Now().AddDate(0, 0, -StoppedConnectionRetentionDays)
	var total int64
	for {
		n, err := s.connectionRepo.PruneStoppedConnectionsBefore(ctx, cutoff, db.StoppedConnectionPruneBatch)
		if err != nil {
			return total, err
		}
		total += n
		if n == 0 {
			break
		}
	}
	return total, nil
}

// StartStoppedPruneLoop periodically deletes long-stopped connection rows
// (StoppedConnectionRetentionDays). Uses a DB lease so only one server runs it.
func (s *Service) StartStoppedPruneLoop(ctx context.Context, leaseRepo *db.TaskLeaseRepo, ownerID string) <-chan struct{} {
	done := make(chan struct{})
	if s == nil || leaseRepo == nil || s.connectionRepo == nil {
		close(done)
		return done
	}
	if s.logger != nil {
		s.logger.Info("Connection stopped-row prune scheduler started",
			"interval", stoppedPruneInterval,
			"retention_days", StoppedConnectionRetentionDays)
	}
	concurrency.SafeGo(s.logger, "connection-stopped-prune", func() {
		defer close(done)
		ticker := time.NewTicker(stoppedPruneInterval)
		defer ticker.Stop()
		s.tryPruneStopped(ctx, leaseRepo, ownerID)
		for {
			select {
			case <-ticker.C:
				s.tryPruneStopped(ctx, leaseRepo, ownerID)
			case <-ctx.Done():
				if s.logger != nil {
					s.logger.Info("Connection stopped-row prune stopped")
				}
				return
			}
		}
	})
	return done
}

func (s *Service) tryPruneStopped(ctx context.Context, leaseRepo *db.TaskLeaseRepo, ownerID string) {
	defer concurrency.SafeRecover(s.logger, "connection-stopped-prune-tick")()
	runCtx, cancel := context.WithTimeout(ctx, stoppedPruneTimeout)
	defer cancel()
	acquired, _, err := leaseRepo.TryAcquire(runCtx, stoppedPruneLeaseName, ownerID, stoppedPruneInterval)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("connection stopped prune: lease acquire failed", "error", err)
		}
		return
	}
	if !acquired {
		return
	}
	n, err := s.pruneOldStoppedConnections(runCtx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("connection stopped prune failed", "error", err)
		}
		return
	}
	if n > 0 && s.logger != nil {
		s.logger.Info("connection stopped prune completed", "count", n, "retention_days", StoppedConnectionRetentionDays)
	}
}
