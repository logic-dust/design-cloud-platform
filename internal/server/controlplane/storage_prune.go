package controlplane

import (
	"context"
	"time"

	"design-cloud-engine/internal/db"
)

func (s *Server) pruneStorageReports(ctx context.Context) {
	if s == nil || s.storage == nil {
		return
	}
	before := time.Now().AddDate(0, 0, -db.StorageReportRetentionDays)
	var total int64
	for {
		deleted, err := s.storage.PruneReportsBefore(ctx, before, db.StorageReportPruneBatch)
		if err != nil {
			s.logger.Warn("Storage report prune: delete batch failed", "error", err, "deleted", total)
			return
		}
		total += deleted
		if deleted < int64(db.StorageReportPruneBatch) {
			break
		}
	}
	if total > 0 {
		s.logger.Info("Pruned old storage reports", "count", total, "retention_days", db.StorageReportRetentionDays)
	}
}
