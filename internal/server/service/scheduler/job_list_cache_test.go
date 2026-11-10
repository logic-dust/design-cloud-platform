package scheduler

import (
	"testing"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/server/cache"
)

func TestJobListCacheKeysDistinct(t *testing.T) {
	t.Parallel()
	clusterKey := jobListCacheKey(JobListQuery{}, schema.JobSchedulerLSF)
	userKey := jobListCacheKey(JobListQuery{User: "cluster"}, schema.JobSchedulerLSF)
	if clusterKey == userKey {
		t.Fatalf("cluster and user cache keys must not collide: %q", clusterKey)
	}
}

func TestInvalidateJobListsKeepsOtherUsers(t *testing.T) {
	t.Parallel()
	svc := &Service{jobCache: cache.NewTTLCache[string, JobListResult](time.Hour)}
	clusterKey := jobListCacheKey(JobListQuery{}, schema.JobSchedulerLSF)
	aliceKey := jobListCacheKey(JobListQuery{User: "alice"}, schema.JobSchedulerLSF)
	aliceQueueKey := jobListCacheKey(JobListQuery{User: "alice", Queue: "q1"}, schema.JobSchedulerLSF)
	bobKey := jobListCacheKey(JobListQuery{User: "bob"}, schema.JobSchedulerLSF)
	aliKey := jobListCacheKey(JobListQuery{User: "ali"}, schema.JobSchedulerLSF)
	for _, k := range []string{clusterKey, aliceKey, aliceQueueKey, bobKey, aliKey} {
		svc.jobCache.SetWithTTL(k, JobListResult{}, 0)
	}

	svc.invalidateJobLists("alice")

	for _, k := range []string{clusterKey, aliceKey, aliceQueueKey} {
		if _, ok := svc.jobCache.GetIfPresent(k); ok {
			t.Fatalf("key %q should be invalidated", k)
		}
	}
	for _, k := range []string{bobKey, aliKey} {
		if _, ok := svc.jobCache.GetIfPresent(k); !ok {
			t.Fatalf("key %q should stay cached", k)
		}
	}
}

func TestJobListCacheKeysIncludeSchedulerType(t *testing.T) {
	t.Parallel()
	q := JobListQuery{Queue: "q1"}
	if jobListCacheKey(q, schema.JobSchedulerLSF) == jobListCacheKey(q, schema.JobSchedulerSlurm) {
		t.Fatal("cluster keys for different scheduler types must differ")
	}
	uq := JobListQuery{User: "alice"}
	if jobListCacheKey(uq, schema.JobSchedulerLSF) == jobListCacheKey(uq, schema.JobSchedulerSlurm) {
		t.Fatal("user keys for different scheduler types must differ")
	}
}

func TestInvalidateJobListsCoversAllSchedulerTypes(t *testing.T) {
	t.Parallel()
	svc := &Service{jobCache: cache.NewTTLCache[string, JobListResult](time.Hour)}
	keys := []string{
		jobListCacheKey(JobListQuery{}, schema.JobSchedulerLSF),
		jobListCacheKey(JobListQuery{}, schema.JobSchedulerSlurm),
		jobListCacheKey(JobListQuery{User: "alice"}, schema.JobSchedulerLSF),
		jobListCacheKey(JobListQuery{User: "alice"}, schema.JobSchedulerSlurm),
	}
	for _, k := range keys {
		svc.jobCache.SetWithTTL(k, JobListResult{}, 0)
	}
	svc.invalidateJobLists("alice")
	for _, k := range keys {
		if _, ok := svc.jobCache.GetIfPresent(k); ok {
			t.Fatalf("key %q should be invalidated", k)
		}
	}
}
