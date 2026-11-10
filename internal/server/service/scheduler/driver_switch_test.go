package scheduler

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/service/settings"
)

func newSwitchTestService(t *testing.T) (*Service, *settings.Service) {
	t.Helper()
	return newSwitchTestServiceWithExecutor(t, &fakeExecutor{})
}

func newSwitchTestServiceWithExecutor(t *testing.T, exec execution.Executor) (*Service, *settings.Service) {
	t.Helper()

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := settings.EnsureDefaults(store); err != nil {
		t.Fatalf("seed defaults: %v", err)
	}
	repos := db.NewRepos(store)
	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(t.TempDir(), "audit.log"),
		SecurityLogPath: filepath.Join(t.TempDir(), "security.log"),
		NodeID:          "test-node",
	})
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Close() })
	settingsSvc, err := settings.NewService(repos.Setting, slog.Default(), auditSvc, nil, repos.CacheVersion, repos.Node)
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}
	svc, err := NewService(exec, settingsSvc, slog.Default(), nil)
	if err != nil {
		t.Fatalf("new scheduler service: %v", err)
	}
	return svc, settingsSvc
}

type fakeExecutor struct{}

func (fakeExecutor) Execute(context.Context, *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	return &execution.ExecuteResponse{Success: true}, nil
}

// TestServiceFollowsSchedulerTypeSetting verifies the driver, metadata and job cache follow
// settings.scheduler.type at runtime instead of being fixed at startup.
func TestServiceFollowsSchedulerTypeSetting(t *testing.T) {
	svc, settingsSvc := newSwitchTestService(t)
	ctx := context.Background()

	meta, err := svc.Meta(ctx)
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	lsfKeys := meta.Keys
	if meta.Scheduler != schema.JobSchedulerLSF {
		t.Fatalf("default scheduler = %q, want lsf", meta.Scheduler)
	}

	svc.jobCache.SetWithTTL(jobListCacheKey(JobListQuery{}, schema.JobSchedulerLSF), JobListResult{Jobs: []map[string]any{{"JOBID": "1"}}}, 1<<40)
	if svc.jobCache.Len() != 1 {
		t.Fatalf("cache not seeded")
	}

	if err := settingsSvc.UpdateByDomain(ctx, schema.DomainScheduler, []byte(`{"type":"slurm"}`)); err != nil {
		t.Fatalf("switch to slurm: %v", err)
	}
	meta, err = svc.Meta(ctx)
	if err != nil {
		t.Fatalf("Meta after switch: %v", err)
	}
	if meta.Scheduler != schema.JobSchedulerSlurm || meta.Keys == lsfKeys {
		t.Errorf("meta did not follow type change: %+v", meta)
	}
	if svc.jobCache.Len() != 0 {
		t.Errorf("job cache must be cleared on type change, len = %d", svc.jobCache.Len())
	}

	if err := settingsSvc.UpdateByDomain(ctx, schema.DomainScheduler, []byte(`{"type":"lsf"}`)); err != nil {
		t.Fatalf("switch back to lsf: %v", err)
	}
	meta, err = svc.Meta(ctx)
	if err != nil || meta.Scheduler != schema.JobSchedulerLSF || meta.Keys != lsfKeys {
		t.Errorf("meta after switching back = %+v, err = %v", meta, err)
	}
}

// slurmListExecutor answers every scheduler query with a one-job squeue --json document.
type slurmListExecutor struct{}

func (slurmListExecutor) Execute(_ context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	return &execution.ExecuteResponse{TraceID: req.TraceID, Success: true,
		Stdout: `{"jobs":[{"job_id":42,"job_state":["RUNNING"],"user_name":"alice","partition":"p1"}]}`}, nil
}

// TestJobListCacheIgnoresStaleDriverResults simulates an in-flight query of the previous
// driver writing its result back after the switch: the new scheduler type must not read it.
func TestJobListCacheIgnoresStaleDriverResults(t *testing.T) {
	svc, settingsSvc := newSwitchTestServiceWithExecutor(t, slurmListExecutor{})
	ctx := context.Background()

	if _, err := svc.Meta(ctx); err != nil { // resolve the default lsf driver
		t.Fatalf("Meta: %v", err)
	}
	if err := settingsSvc.UpdateByDomain(ctx, schema.DomainScheduler, []byte(`{"type":"slurm"}`)); err != nil {
		t.Fatalf("switch to slurm: %v", err)
	}
	if _, err := svc.Meta(ctx); err != nil { // swap to slurm, clearing the cache
		t.Fatalf("Meta after switch: %v", err)
	}

	stale := JobListResult{Jobs: []map[string]any{{"JOBID": "1", "STAT": "RUN"}}}
	svc.jobCache.SetWithTTL(jobListCacheKey(JobListQuery{}, schema.JobSchedulerLSF), stale, 1<<40)
	svc.jobCache.SetWithTTL(jobListCacheKey(JobListQuery{User: "alice"}, schema.JobSchedulerLSF), stale, 1<<40)

	cluster, err := svc.ListJobs(ctx, JobListQuery{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	mine, err := svc.ListJobs(ctx, JobListQuery{User: "alice"})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	for name, res := range map[string]JobListResult{"cluster": cluster, "user": mine} {
		if res.Scheduler != schema.JobSchedulerSlurm {
			t.Fatalf("%s scheduler = %q, want slurm", name, res.Scheduler)
		}
		if len(res.Jobs) != 1 {
			t.Fatalf("%s jobs = %v, want the single slurm job", name, res.Jobs)
		}
		if _, ok := res.Jobs[0]["JOBID"]; ok {
			t.Fatalf("%s returned the stale lsf record: %v", name, res.Jobs[0])
		}
		if res.Jobs[0]["job_id"] == nil {
			t.Fatalf("%s record lacks slurm job_id: %v", name, res.Jobs[0])
		}
	}
}
