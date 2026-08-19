package storage

import (
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

// Service provides shared storage monitoring data.
type Service struct {
	storageRepo    *db.StorageRepo
	collectTrigger contract.CollectTrigger
}

// NewService creates a new storage monitoring service.
func NewService(storageRepo *db.StorageRepo, t contract.CollectTrigger) *Service {
	return &Service{
		storageRepo:    storageRepo,
		collectTrigger: t,
	}
}

// TriggerCollect triggers an on-demand shared storage collection probe across the cluster.
func (s *Service) TriggerCollect(ctx context.Context, target contract.CollectTarget) error {
	if s.collectTrigger == nil {
		return fmt.Errorf("storage collect trigger is not configured")
	}
	return s.collectTrigger.TriggerCollect(ctx, target)
}

// GetLatestReports returns the latest storage report for each shared storage array
// within the given time window.
func (s *Service) GetLatestReports(ctx context.Context, windowSeconds int) (map[string]model.StorageReport, error) {
	return s.storageRepo.QueryLatestStorageReports(ctx, windowSeconds)
}

// GetHistory returns historical capacity samples for a specific storage array.
func (s *Service) GetHistory(ctx context.Context, storageID string, from, to time.Time) ([]model.StorageReport, error) {
	return s.storageRepo.QueryStorageHistory(ctx, storageID, from, to)
}
