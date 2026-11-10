package db

import (
	"context"
	"slices"
	"time"

	"design-cloud-engine/internal/model"
)

// storageProbeDedupSeconds prevents duplicate storage reports from different
// agents probing the same shared storage near-simultaneously. 5 minutes is
// more than enough — multi-agent clock skew rarely exceeds seconds.
const (
	storageProbeDedupSeconds   = 300
	StorageReportRetentionDays = 90
	StorageReportPruneBatch    = 500
)

// maxStorageHistoryQueryRows caps history responses; var so tests can lower it.
var maxStorageHistoryQueryRows = 10000

type StorageRepo struct {
	store *Store
}

// InsertStorageReport persists a new capacity sample. Reports for the
// same storage_id within the dedup window are silently dropped.
//
// The dedup check and the insert are combined into a single atomic statement
// (WHERE NOT EXISTS ... SELECT ...) so two concurrent probes of the same
// shared storage cannot both pass the check and both insert a duplicate
// (a TOCTOU race in the previous read-then-write implementation).
// The idx_storage_reported (storage_id, reported_at) index keeps the NOT
// EXISTS subquery to O(1) lookups.
func (s *StorageRepo) InsertStorageReport(ctx context.Context, report *model.StorageReport) error {
	cutoff := report.ReportedAt.Add(-storageProbeDedupSeconds * time.Second)

	return s.store.db.WithContext(ctx).Exec(`
		INSERT INTO storage_reports
			(storage_id, total, used, reported_at, sampled_by)
		SELECT ?, ?, ?, ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM storage_reports
			WHERE storage_id = ? AND reported_at > ?
		)
	`,
		report.StorageID,
		report.Total,
		report.Used,
		report.ReportedAt,
		report.SampledBy,
		report.StorageID,
		cutoff,
	).Error
}

// QueryStorageHistory retrieves historical capacity samples for a specific storage array
// within the given time range. When the window exceeds maxStorageHistoryQueryRows,
// the newest rows are kept (DESC then reverse to chronological ASC for callers).
func (s *StorageRepo) QueryStorageHistory(ctx context.Context, storageID string, from, to time.Time) ([]model.StorageReport, error) {
	var reports []model.StorageReport
	err := s.store.db.WithContext(ctx).
		Where("storage_id = ? AND reported_at BETWEEN ? AND ?", storageID, from, to).
		Order("reported_at DESC").
		Limit(maxStorageHistoryQueryRows).
		Find(&reports).Error
	if err != nil {
		return nil, err
	}
	slices.Reverse(reports)
	return reports, nil
}

// PruneReportsBefore deletes storage samples older than cutoff in batches.
// GORM ignores Limit on Delete, so ids are plucked first (same pattern as ExecutionRepo.PruneTerminal).
func (s *StorageRepo) PruneReportsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if err := validateLimit(limit); err != nil {
		return 0, err
	}
	var ids []uint
	if err := s.store.db.WithContext(ctx).Model(&model.StorageReport{}).
		Where("reported_at < ?", before).
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := s.store.db.WithContext(ctx).Delete(&model.StorageReport{}, ids)
	return result.RowsAffected, result.Error
}

// QueryLatestStorageReports retrieves the most recent sample for each shared storage array
// within the given time window.
func (s *StorageRepo) QueryLatestStorageReports(ctx context.Context, windowSeconds int) (map[string]model.StorageReport, error) {
	var results []model.StorageReport

	cutoff := time.Now().Add(-time.Duration(windowSeconds) * time.Second)

	query := `
		SELECT r.*
		FROM storage_reports r
		INNER JOIN (
			SELECT storage_id, MAX(reported_at) as max_reported_at
			FROM storage_reports
			WHERE reported_at > ?
			GROUP BY storage_id
		) latest ON r.storage_id = latest.storage_id AND r.reported_at = latest.max_reported_at
	`

	if err := s.store.db.WithContext(ctx).Raw(query, cutoff).Scan(&results).Error; err != nil {
		return nil, err
	}

	latestMap := make(map[string]model.StorageReport, len(results))
	for _, r := range results {
		latestMap[r.StorageID] = r
	}

	return latestMap, nil
}
