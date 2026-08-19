package db

import (
	"context"
	"time"

	"design-cloud-engine/internal/model"
)

// storageProbeDedupSeconds prevents duplicate storage reports from different
// agents probing the same shared storage near-simultaneously. 5 minutes is
// more than enough — multi-agent clock skew rarely exceeds seconds.
const storageProbeDedupSeconds = 300

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
// within the given time range.
func (s *StorageRepo) QueryStorageHistory(ctx context.Context, storageID string, from, to time.Time) ([]model.StorageReport, error) {
	var reports []model.StorageReport
	err := s.store.db.WithContext(ctx).
		Where("storage_id = ? AND reported_at BETWEEN ? AND ?", storageID, from, to).
		Order("reported_at ASC").
		Find(&reports).Error
	return reports, err
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
