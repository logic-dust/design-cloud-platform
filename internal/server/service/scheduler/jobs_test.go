package scheduler

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/push"
)

type staticSettingsReader struct {
	st schema.SchedulerSettings
}

func (r staticSettingsReader) GetSchedulerSettings(context.Context) (schema.SchedulerSettings, error) {
	return r.st, nil
}

func newTestService(name schema.JobScheduler) *Service {
	drv := newDriver(name)
	if drv == nil {
		return nil
	}
	meta := drv.Meta()
	meta.Scheduler = name
	return &Service{
		settingsSvc: staticSettingsReader{st: schema.SchedulerSettings{Type: name}},
		ops:         drv,
		opsType:     name,
		meta:        meta,
	}
}

type fakeSchedulerDriver struct {
	controlErr    error
	controlCalls  int
	lastSubmit    JobSubmitRequest
	listResult    JobListResult
	listErr       error
	listQueuesErr error
	lastListQuery JobListQuery
}

func (d *fakeSchedulerDriver) Meta() SchedulerMeta {
	return SchedulerMeta{Keys: SchedulerKeyMap{Status: "status"}}
}

func (d *fakeSchedulerDriver) ExecuteListJobs(_ context.Context, _ execution.Executor, query JobListQuery) (JobListResult, error) {
	d.lastListQuery = query
	if d.listErr != nil {
		return JobListResult{}, d.listErr
	}
	if d.listResult.Jobs != nil {
		return d.listResult, nil
	}
	return JobListResult{}, nil
}

func (d *fakeSchedulerDriver) ExecuteListQueues(context.Context, execution.Executor) (QueueListResult, error) {
	if d.listQueuesErr != nil {
		return QueueListResult{}, d.listQueuesErr
	}
	return QueueListResult{}, nil
}

func (d *fakeSchedulerDriver) ExecuteListHosts(context.Context, execution.Executor) (HostListResult, error) {
	return HostListResult{}, nil
}

func (d *fakeSchedulerDriver) NormalizeStatus(raw string) NormalizedJobStatus {
	return JobStatusRunning
}

func (d *fakeSchedulerDriver) ExecuteGetJob(context.Context, execution.Executor, string) (map[string]any, error) {
	return nil, nil
}

func (d *fakeSchedulerDriver) ExecuteSubmit(ctx context.Context, exec execution.Executor, req JobSubmitRequest, targetNode string, nodeAffinity execution.NodeAffinity) (JobSubmitResult, error) {
	d.lastSubmit = req
	return JobSubmitResult{}, nil
}

func (d *fakeSchedulerDriver) ExecuteControl(context.Context, execution.Executor, string, JobOp, string, JobControlArgs) error {
	d.controlCalls++
	return d.controlErr
}

func (d *fakeSchedulerDriver) ExecuteModify(context.Context, execution.Executor, string, JobModifyArgs, string) error {
	return nil
}

func TestControlJobNormalizesJobNotFound(t *testing.T) {
	t.Parallel()
	service := &Service{ops: &fakeSchedulerDriver{controlErr: fmt.Errorf("job <123> is not found")}}
	failures, err := service.ControlJob(context.Background(), JobControlRequest{
		JobIDs:      []string{"123"},
		Operation:   JobOpKill,
		RequestUser: "hpcadmin",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if failures["123"] == "" {
		t.Fatalf("expected failure for job 123")
	}
	if !strings.Contains(failures["123"], "not found") {
		t.Fatalf("failure message = %q, want it to contain 'not found'", failures["123"])
	}
}

func TestControlJobRejectsMoreThanMaxBatchControlJobs(t *testing.T) {
	t.Parallel()
	driver := &fakeSchedulerDriver{}
	svc := &Service{ops: driver}
	jobIDs := make([]string, MaxBatchControlJobs+1)
	for i := range jobIDs {
		jobIDs[i] = fmt.Sprintf("%d", i+1)
	}

	_, err := svc.ControlJob(context.Background(), JobControlRequest{
		JobIDs:    jobIDs,
		Operation: JobOpKill,
	})
	if err == nil {
		t.Fatal("expected batch size error")
	}
	domainErr, ok := errors.AsType[contract.DomainError](err)
	if !ok || domainErr.HTTPStatus() != 400 {
		t.Fatalf("error = %v, want HTTP 400 domain error", err)
	}
	if driver.controlCalls != 0 {
		t.Fatalf("scheduler control calls = %d, want 0", driver.controlCalls)
	}
}

func TestControlJobAllowsMaxBatchControlJobs(t *testing.T) {
	t.Parallel()
	driver := &fakeSchedulerDriver{}
	svc := &Service{
		ops:      driver,
		jobCache: cache.NewTTLCache[string, JobListResult](time.Minute),
	}
	jobIDs := make([]string, MaxBatchControlJobs)
	for i := range jobIDs {
		jobIDs[i] = fmt.Sprintf("%d", i+1)
	}

	if _, err := svc.ControlJob(context.Background(), JobControlRequest{
		JobIDs:    jobIDs,
		Operation: JobOpKill,
	}); err != nil {
		t.Fatalf("ControlJob at max batch size: %v", err)
	}
	if driver.controlCalls != MaxBatchControlJobs {
		t.Fatalf("scheduler control calls = %d, want %d", driver.controlCalls, MaxBatchControlJobs)
	}
}

func TestNormalizeJobStatus(t *testing.T) {
	t.Parallel()

	// fakeSchedulerDriver.Meta().Keys.Status is "status", NormalizeStatus always returns JobStatusRunning.
	svc := &Service{ops: &fakeSchedulerDriver{}}

	cases := []struct {
		name string
		job  map[string]any
		want NormalizedJobStatus
	}{
		{"field present with value", map[string]any{"status": "anything"}, JobStatusRunning},
		{"field present empty string", map[string]any{"status": ""}, JobStatusRunning},
		{"field missing", map[string]any{"other": "RUN"}, JobStatusRunning},
		{"nil ops", nil, JobStatusUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got NormalizedJobStatus
			if tc.job == nil {
				// nil ops path
				got = (&Service{}).NormalizeJobStatus(map[string]any{})
			} else {
				got = svc.NormalizeJobStatus(tc.job)
			}
			if got != tc.want {
				t.Errorf("NormalizeJobStatus(%v) = %q, want %q", tc.job, got, tc.want)
			}
		})
	}
}

type fakePublisher struct {
	published []push.Message
	users     []string
}

func (p *fakePublisher) Broadcast(_ context.Context, msg push.Message) {
	p.published = append(p.published, msg)
}

func (p *fakePublisher) PublishToUser(_ context.Context, users []string, msg push.Message) {
	p.users = append(p.users, users...)
	p.published = append(p.published, msg)
}

func (p *fakePublisher) PublishToRole(_ context.Context, _ model.Role, msg push.Message) {
	p.published = append(p.published, msg)
}

func TestControlJobDoesNotPushToOperator(t *testing.T) {
	t.Parallel()
	pub := &fakePublisher{}
	service := &Service{
		ops:       &fakeSchedulerDriver{},
		publisher: pub,
		jobCache:  cache.NewTTLCache[string, JobListResult](time.Minute),
	}
	_, err := service.ControlJob(context.Background(), JobControlRequest{
		JobIDs:      []string{"123"},
		Operation:   JobOpKill,
		RequestUser: "alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pub.published) != 0 {
		t.Fatalf("sync job control must not push to operator; got %d messages", len(pub.published))
	}
}

func TestModifyJobDoesNotPushToOperator(t *testing.T) {
	t.Parallel()
	pub := &fakePublisher{}
	service := &Service{
		ops:       &fakeSchedulerDriver{},
		publisher: pub,
		jobCache:  cache.NewTTLCache[string, JobListResult](time.Minute),
	}
	name := "new-name"
	err := service.ModifyJob(context.Background(), "456", JobModifyArgs{Name: &name}, "bob")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pub.published) != 0 {
		t.Fatalf("sync job modify must not push to operator; got %d messages", len(pub.published))
	}
}

func TestControlJobRejectsInvalidInputBeforeExecuting(t *testing.T) {
	t.Parallel()
	cases := []JobControlRequest{
		{JobIDs: []string{"1", "0"}, Operation: JobOpKill, RequestUser: "u"},
		{JobIDs: []string{"123[1]"}, Operation: JobOpKill, RequestUser: "u"},
		{JobIDs: nil, Operation: JobOpKill, RequestUser: "u"},
		{JobIDs: []string{"1"}, Operation: JobOpSwitch, TargetQueue: "", RequestUser: "u"},
		{JobIDs: []string{"1"}, Operation: JobOpSwitch, TargetQueue: "-m", RequestUser: "u"},
	}
	for _, req := range cases {
		driver := &fakeSchedulerDriver{}
		svc := &Service{ops: driver}
		_, err := svc.ControlJob(context.Background(), req)
		domainErr, ok := errors.AsType[contract.DomainError](err)
		if !ok || domainErr.HTTPStatus() != 400 {
			t.Fatalf("req %+v: error = %v, want HTTP 400", req, err)
		}
		if driver.controlCalls != 0 {
			t.Fatalf("req %+v: control calls = %d, want 0", req, driver.controlCalls)
		}
	}
}

func TestModifyJobValidatesArgs(t *testing.T) {
	t.Parallel()
	svc := &Service{ops: &fakeSchedulerDriver{}}
	bad := "-x"
	empty := " "
	good := "renamed"
	for _, tc := range []struct {
		id   string
		args JobModifyArgs
	}{
		{"0", JobModifyArgs{Name: &good}},
		{"1", JobModifyArgs{}},
		{"1", JobModifyArgs{Name: &bad}},
		{"1", JobModifyArgs{Name: &empty}},
		{"1", JobModifyArgs{Queue: &bad}},
	} {
		err := svc.ModifyJob(context.Background(), tc.id, tc.args, "u")
		domainErr, ok := errors.AsType[contract.DomainError](err)
		if !ok || domainErr.HTTPStatus() != 400 {
			t.Fatalf("ModifyJob(%q, %+v) error = %v, want HTTP 400", tc.id, tc.args, err)
		}
	}
	if err := svc.ModifyJob(context.Background(), "1", JobModifyArgs{Name: &good}, "u"); err != nil {
		t.Fatalf("valid modify: %v", err)
	}
}

func mustMeta(t *testing.T, svc *Service) SchedulerMeta {
	t.Helper()
	meta, err := svc.Meta(context.Background())
	if err != nil {
		t.Fatalf("Meta() failed: %v", err)
	}
	return meta
}

func TestServiceMeta(t *testing.T) {
	t.Parallel()

	for _, name := range []schema.JobScheduler{
		schema.JobSchedulerLSF,
		schema.JobSchedulerSlurm,
		schema.JobSchedulerOpenLava,
	} {
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()
			drv := newDriver(name)
			if drv == nil {
				t.Fatalf("newDriver(%q) returned nil", name)
			}
			svc := newTestService(name)
			meta := mustMeta(t, svc)
			if meta.Scheduler != name {
				t.Errorf("meta.Scheduler = %q, want %q", meta.Scheduler, name)
			}
			if len(meta.Fields.Job) == 0 || len(meta.Fields.Host) == 0 || len(meta.Fields.Queue) == 0 {
				t.Errorf("meta.Fields incomplete: %+v", meta.Fields)
			}
			if meta.Keys.ID == "" || meta.Keys.User == "" || meta.Keys.Status == "" || meta.Keys.SubmitTime == "" {
				t.Errorf("meta.Keys has empty fields: %+v", meta.Keys)
			}
			if len(meta.Actions) == 0 {
				t.Error("meta.Actions is empty")
			}
			view := mustMeta(t, svc)
			view.Actions[JobOpKill] = append(view.Actions[JobOpKill], "MUTATED")
			if slices.Contains(mustMeta(t, svc).Actions[JobOpKill], "MUTATED") {
				t.Error("Service.Meta() returned an alias of the cached action table")
			}
			if name == schema.JobSchedulerOpenLava {
				lsfDrv := newDriver(schema.JobSchedulerLSF)
				lsfMeta := lsfDrv.Meta()
				if !slices.Equal(meta.Fields.Job, lsfMeta.Fields.Job) ||
					!slices.Equal(meta.Fields.Host, lsfMeta.Fields.Host) ||
					!slices.Equal(meta.Fields.Queue, lsfMeta.Fields.Queue) {
					t.Errorf("openlava fields = %+v, want %+v", meta.Fields, lsfMeta.Fields)
				}
				if meta.Keys != lsfMeta.Keys {
					t.Errorf("openlava keys = %+v, want %+v", meta.Keys, lsfMeta.Keys)
				}
				if !maps.EqualFunc(mustMeta(t, svc).Actions, lsfMeta.Actions, slices.Equal) {
					t.Errorf("openlava actions = %#v, want %#v", mustMeta(t, svc).Actions, lsfMeta.Actions)
				}
			}
		})
	}
}

func TestListJobsStampsScheduler(t *testing.T) {
	t.Parallel()

	for _, name := range []schema.JobScheduler{schema.JobSchedulerLSF, schema.JobSchedulerSlurm} {
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()
			driver := &fakeSchedulerDriver{
				listResult: JobListResult{Jobs: []map[string]any{{"id": "1"}}},
			}
			svc := &Service{
				ops:      driver,
				opsType:  name,
				meta:     SchedulerMeta{Scheduler: name},
				jobCache: cache.NewTTLCache[string, JobListResult](time.Minute),
			}

			got, err := svc.ListJobs(context.Background(), JobListQuery{})
			if err != nil {
				t.Fatalf("ListJobs: %v", err)
			}
			if got.Scheduler != name {
				t.Fatalf("ListJobs scheduler = %q, want %q", got.Scheduler, name)
			}

			gotUser, err := svc.ListJobs(context.Background(), JobListQuery{User: "alice"})
			if err != nil {
				t.Fatalf("ListJobs: %v", err)
			}
			if gotUser.Scheduler != name {
				t.Fatalf("ListJobs scheduler = %q, want %q", gotUser.Scheduler, name)
			}
		})
	}
}

func TestListQueuesPropagatesListError(t *testing.T) {
	t.Parallel()
	listErr := errors.New("bqueues failed")
	svc := &Service{
		ops:  &fakeSchedulerDriver{listQueuesErr: listErr},
		meta: SchedulerMeta{Scheduler: schema.JobSchedulerLSF},
	}
	if _, err := svc.ListQueues(context.Background()); !errors.Is(err, listErr) {
		t.Fatalf("ListQueues err = %v, want %v", err, listErr)
	}
}
