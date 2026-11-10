package scheduler

import (
	"context"
	"testing"
	"time"

	"design-cloud-engine/internal/server/cache"
)

func TestJobStatusCountsForUser(t *testing.T) {
	t.Parallel()
	driver := &fakeSchedulerDriver{
		listResult: JobListResult{
			Jobs: []map[string]any{
				{"status": "a"},
				{"status": "b"},
			},
		},
	}
	svc := &Service{
		ops:      driver,
		jobCache: cache.NewTTLCache[string, JobListResult](time.Minute),
	}
	counts, err := svc.JobStatusCounts(context.Background(), JobListQuery{User: "alice"})
	if err != nil {
		t.Fatalf("JobStatusCounts: %v", err)
	}
	if counts.Running != 2 {
		t.Fatalf("counts = %+v, want 2 running (fake driver normalizes to running)", counts)
	}
	if driver.lastListQuery.User != "alice" {
		t.Fatalf("list query user = %q, want alice", driver.lastListQuery.User)
	}
}

func TestAggregateJobStatusCounts(t *testing.T) {
	t.Parallel()
	svc := &Service{ops: newLSFDriver()}
	jobs := []map[string]any{
		{"STAT": "DONE"},
		{"STAT": "EXIT"},
		{"STAT": "PSUSP"},
	}
	counts := aggregateJobStatusCounts(svc, jobs)
	if counts.Completed != 1 || counts.Failed != 1 || counts.Suspended != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}
