package overview

import (
	"context"

	"design-cloud-engine/internal/server/service/scheduler"
)

// JobsStatsMode selects how scheduler.jobs is aggregated in an overview response.
type JobsStatsMode int

const (
	JobsStatsCluster     JobsStatsMode = 0 // all users (/api/overview/cluster)
	JobsStatsSessionUser JobsStatsMode = 1 // current session user (/api/overview/me)
)

func jobStatsFromScheduler(c scheduler.JobStatusCounts) JobStats {
	return JobStats{
		Running:   c.Running,
		Pending:   c.Pending,
		Suspended: c.Suspended,
		Completed: c.Completed,
		Failed:    c.Failed,
	}
}

func (s *Service) schedulerJobsForMode(ctx context.Context, cached JobStats, mode JobsStatsMode, sessionUser string) (JobStats, error) {
	if mode == JobsStatsCluster {
		return cached, nil
	}
	if sessionUser == "" {
		return JobStats{}, nil
	}
	counts, err := s.schedulerSvc.JobStatusCounts(ctx, scheduler.JobListQuery{User: sessionUser})
	if err != nil {
		return JobStats{}, err
	}
	return jobStatsFromScheduler(counts), nil
}
