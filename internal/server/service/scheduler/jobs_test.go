package scheduler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/cache"
	"design-cloud-engine/internal/server/service/push"
)

type fakeSchedulerDriver struct {
	controlErr error
	lastSubmit JobSubmitRequest
}

func (d *fakeSchedulerDriver) JobFields() []string { return nil }
func (d *fakeSchedulerDriver) StatusKey() string   { return "status" }

func (d *fakeSchedulerDriver) ExecuteListJobs(context.Context, execution.Executor, JobListQuery) (JobListResult, error) {
	return JobListResult{}, nil
}

func (d *fakeSchedulerDriver) ExecuteListQueues(context.Context, execution.Executor, []map[string]any) (QueueListResult, error) {
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

func TestNormalizeJobStatus(t *testing.T) {
	t.Parallel()

	// fakeSchedulerDriver.StatusKey() returns "status", NormalizeStatus always returns JobStatusRunning.
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

func TestControlJobPublishesJobEvent(t *testing.T) {
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
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(pub.published))
	}
	if pub.published[0].Name != push.EventJob {
		t.Errorf("published event = %v, want %v", pub.published[0].Name, push.EventJob)
	}
	if len(pub.users) == 0 || pub.users[0] != "alice" {
		t.Errorf("published users = %v, want ['alice']", pub.users)
	}
}

func TestModifyJobPublishesJobEvent(t *testing.T) {
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
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(pub.published))
	}
	if pub.published[0].Name != push.EventJob {
		t.Errorf("published event = %v, want %v", pub.published[0].Name, push.EventJob)
	}
	ep, ok := pub.published[0].Payload.(push.EventPayload)
	if !ok || ep.TargetID != "456" {
		t.Errorf("expected TargetID = '456', got %+v", pub.published[0].Payload)
	}
}
