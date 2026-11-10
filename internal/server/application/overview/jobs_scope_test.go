package overview

import (
	"testing"

	"design-cloud-engine/internal/server/service/scheduler"
)

func TestJobStatsFromScheduler(t *testing.T) {
	t.Parallel()
	got := jobStatsFromScheduler(scheduler.JobStatusCounts{Running: 1, Failed: 2})
	if got.Running != 1 || got.Failed != 2 {
		t.Fatalf("got %+v", got)
	}
}
