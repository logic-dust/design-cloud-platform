package dashboard

import (
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"design-cloud-engine/internal/server/service/scheduler"
)

// =============================================================================
// RefreshJobs Tests
// =============================================================================

func TestRefreshJobs(t *testing.T) {
	t.Parallel()

	svc := &Service{
		logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}

	tests := []struct {
		name          string
		jobsResult    scheduler.JobListResult
		err           error
		wantRunning   int
		wantPending   int
		wantSuspended int
		wantCompleted int
		wantFailed    int
	}{
		{
			name: "mixed statuses",
			jobsResult: scheduler.JobListResult{
				Fields: []string{"job_id", "status"},
				Jobs: []map[string]any{
					{"job_id": "1", "status": "RUN"},
					{"job_id": "2", "status": "running"},
					{"job_id": "3", "status": "R"},
					{"job_id": "4", "status": "PEND"},
					{"job_id": "5", "status": "pending"},
					{"job_id": "6", "status": "PD"},
					{"job_id": "7", "status": "PSUSP"},
					{"job_id": "8", "status": "COMPLETED"},
					{"job_id": "9", "status": "FAILED"},
				},
			},
			wantRunning:   3,
			wantPending:   3,
			wantSuspended: 1,
			wantCompleted: 1,
			wantFailed:    1,
		},
		{
			name:        "error from scheduler",
			err:         errors.New("scheduler connection failed"),
			wantRunning: 0,
			wantPending: 0,
		},
		{
			name: "missing status field in header",
			jobsResult: scheduler.JobListResult{
				Fields: []string{"job_id", "name"},
				Jobs: []map[string]any{
					{"job_id": "1", "name": "test"},
				},
			},
			wantRunning: 0,
			wantPending: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc.schedulerCache.Set(SchedulerStats{}, time.Now())

			if tc.err == nil && len(tc.jobsResult.Fields) > 0 {
				var running, pending, suspended, completed, failed int
				for _, job := range tc.jobsResult.Jobs {
					if statVal, ok := job["status"].(string); ok {
						switch statVal {
						case "RUN", "running", "R":
							running++
						case "PEND", "pending", "PD":
							pending++
						case "PSUSP", "SSUSP", "USUSP", "SUSPENDED":
							suspended++
						case "COMPLETED", "DONE":
							completed++
						case "FAILED", "EXIT":
							failed++
						}
					}
				}
				if running != tc.wantRunning || pending != tc.wantPending || suspended != tc.wantSuspended || completed != tc.wantCompleted || failed != tc.wantFailed {
					t.Errorf("running=%d, pending=%d, suspended=%d, completed=%d, failed=%d, want running=%d, pending=%d, suspended=%d, completed=%d, failed=%d",
						running, pending, suspended, completed, failed, tc.wantRunning, tc.wantPending, tc.wantSuspended, tc.wantCompleted, tc.wantFailed)
				}
			}
		})
	}
}
