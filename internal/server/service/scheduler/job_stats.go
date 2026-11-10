package scheduler

import "context"

// JobStatusCounts summarizes jobs by normalized status (overview and other aggregators).
type JobStatusCounts struct {
	Running   int
	Pending   int
	Suspended int
	Completed int
	Failed    int
}

// JobStatusCounts returns job status totals. Empty query.User means cluster-wide.
func (s *Service) JobStatusCounts(ctx context.Context, query JobListQuery) (JobStatusCounts, error) {
	if ops, _, err := s.driver(ctx); err != nil {
		return JobStatusCounts{}, err
	} else if ops == nil {
		return JobStatusCounts{}, nil
	}
	result, err := s.ListJobs(ctx, query)
	if err != nil {
		return JobStatusCounts{}, err
	}
	return aggregateJobStatusCounts(s, result.Jobs), nil
}

func aggregateJobStatusCounts(s *Service, jobs []map[string]any) JobStatusCounts {
	var counts JobStatusCounts
	for _, job := range jobs {
		switch s.NormalizeJobStatus(job) {
		case JobStatusRunning:
			counts.Running++
		case JobStatusPending:
			counts.Pending++
		case JobStatusSuspended:
			counts.Suspended++
		case JobStatusCompleted:
			counts.Completed++
		case JobStatusFailed:
			counts.Failed++
		}
	}
	return counts
}
