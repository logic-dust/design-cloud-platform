package scheduler

import (
	"context"
	"testing"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

type mockExecutor struct {
	lastReq *execution.ExecuteRequest
	resp    *execution.ExecuteResponse
	err     error
}

func (m *mockExecutor) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	m.lastReq = req
	if m.resp != nil {
		return m.resp, m.err
	}
	return &execution.ExecuteResponse{Success: true, Stdout: "Job <123> is submitted to queue <normal>"}, m.err
}

func TestLSFExecuteSubmit(t *testing.T) {
	driver := newLSFDriver("/path/to/profile.lsf")
	exec := &mockExecutor{}

	req := JobSubmitRequest{
		User:  "testuser",
		Name:  "test-job",
		Queue: "my-queue",
		Cmd:   "sleep 10",
	}

	_, err := driver.ExecuteSubmit(context.Background(), exec, req, "", execution.NodeAffinityLocalPreferred)
	if err != nil {
		t.Fatalf("ExecuteSubmit failed: %v", err)
	}

	if exec.lastReq == nil {
		t.Fatalf("Executor was not called")
	}

	jobPayload, ok := exec.lastReq.Payload.(payload.JobSubmit)
	if !ok {
		t.Fatalf("Payload is not ActionJobSubmitPayload")
	}

	if jobPayload.Queue != "my-queue" {
		t.Fatalf("Payload.Queue = %s, want my-queue", jobPayload.Queue)
	}

	// Verify command arguments
	cmd := jobPayload.Command
	found := false
	for i, arg := range cmd.Args {
		if arg == "-q" && i+1 < len(cmd.Args) && cmd.Args[i+1] == "my-queue" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Command args do not contain -q my-queue: %v", cmd.Args)
	}
}

func TestLSFExecuteSubmitEmptyQueue(t *testing.T) {
	driver := newLSFDriver("/path/to/profile.lsf")
	exec := &mockExecutor{}

	req := JobSubmitRequest{
		User: "testuser",
		Name: "test-job",
		Cmd:  "sleep 10",
	}

	_, err := driver.ExecuteSubmit(context.Background(), exec, req, "", execution.NodeAffinityLocalPreferred)
	if err != nil {
		t.Fatalf("ExecuteSubmit failed: %v", err)
	}

	jobPayload := exec.lastReq.Payload.(payload.JobSubmit)
	cmd := jobPayload.Command
	for _, arg := range cmd.Args {
		if arg == "-q" {
			t.Fatalf("Command args contain -q when queue is empty: %v", cmd.Args)
		}
	}
}

func TestLSFExecuteModifyQueue(t *testing.T) {
	driver := newLSFDriver("/path/to/profile.lsf")
	exec := &mockExecutor{}

	args := JobModifyArgs{Queue: new("new-queue")}
	err := driver.ExecuteModify(context.Background(), exec, "123", args, "testuser")
	if err != nil {
		t.Fatalf("ExecuteModify failed: %v", err)
	}

	if exec.lastReq == nil {
		t.Fatalf("Executor was not called")
	}

	jobPayload, ok := exec.lastReq.Payload.(payload.JobModify)
	if !ok {
		t.Fatalf("Payload is not ActionJobModifyPayload")
	}

	if jobPayload.Queue == nil || *jobPayload.Queue != "new-queue" {
		t.Fatalf("Payload.Queue = %v, want 'new-queue'", jobPayload.Queue)
	}

	cmd := jobPayload.Command
	if cmd.Program != "bmod" {
		t.Fatalf("Program = %s, want bmod", cmd.Program)
	}
}

func TestLSFExecuteModifyName(t *testing.T) {
	driver := newLSFDriver("/path/to/profile.lsf")
	exec := &mockExecutor{}

	args := JobModifyArgs{Name: new("new-name")}
	err := driver.ExecuteModify(context.Background(), exec, "123", args, "testuser")
	if err != nil {
		t.Fatalf("ExecuteModify failed: %v", err)
	}

	jobPayload, ok := exec.lastReq.Payload.(payload.JobModify)
	if !ok {
		t.Fatalf("Payload is not ActionJobModifyPayload")
	}

	if jobPayload.Name == nil || *jobPayload.Name != "new-name" {
		t.Fatalf("Payload.Name = %v, want 'new-name'", jobPayload.Name)
	}

	cmd := jobPayload.Command
	if cmd.Program != "bmod" {
		t.Fatalf("Program = %s, want bmod", cmd.Program)
	}
}

func TestLSFExecuteModifyNoArgs(t *testing.T) {
	driver := newLSFDriver("/path/to/profile.lsf")
	exec := &mockExecutor{}

	args := JobModifyArgs{}
	err := driver.ExecuteModify(context.Background(), exec, "123", args, "testuser")
	if err == nil {
		t.Fatal("ExecuteModify should fail with no args")
	}
}

func TestLSFExecuteListJobsEmptyUser(t *testing.T) {
	driver := newLSFDriver("/path/to/profile.lsf")
	exec := &mockExecutor{
		resp: &execution.ExecuteResponse{Success: true, Stdout: `{"JOBS":0,"RECORDS":[]}`},
	}

	_, err := driver.ExecuteListJobs(context.Background(), exec, JobListQuery{})
	if err != nil {
		t.Fatalf("ExecuteListJobs failed: %v", err)
	}

	jobPayload, ok := exec.lastReq.Payload.(payload.JobQuery)
	if !ok {
		t.Fatalf("Payload is not ActionJobQueryPayload")
	}

	cmd := jobPayload.Command
	foundUserAll := false
	for i, arg := range cmd.Args {
		if arg == "-u" && i+1 < len(cmd.Args) && cmd.Args[i+1] == "all" {
			foundUserAll = true
			break
		}
	}
	if !foundUserAll {
		t.Fatalf("Command args do not contain -u all: %v", cmd.Args)
	}
}

func TestLSFNormalizeStatus(t *testing.T) {
	t.Parallel()
	driver := newLSFDriver("")

	cases := []struct {
		raw  string
		want NormalizedJobStatus
	}{
		{"RUN", JobStatusRunning},
		{"run", JobStatusRunning},
		{"PEND", JobStatusPending},
		{"WAIT", JobStatusPending},
		{"PSUSP", JobStatusSuspended},
		{"SSUSP", JobStatusSuspended},
		{"USUSP", JobStatusSuspended},
		{"DONE", JobStatusCompleted},
		{"EXIT", JobStatusFailed},
		{"UNKWN", JobStatusFailed},
		{"ZOMBI", JobStatusFailed},
		// Unknown statuses must return JobStatusUnknown, not be guessed.
		{"RUNNING", JobStatusUnknown},
		{"COMPLETED", JobStatusUnknown},
		{"GARBAGE", JobStatusUnknown},
		{"", JobStatusUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			got := driver.NormalizeStatus(tc.raw)
			if got != tc.want {
				t.Errorf("NormalizeStatus(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseLSFHostsOutput(t *testing.T) {
	t.Parallel()

	jsonStr := `{
		"COMMAND": "bhosts",
		"HOSTS": 2,
		"RECORDS": [
			{"HOST_NAME": "cn01", "STATUS": "ok", "MAX": "16", "NJOBS": "4", "RUN": "4"},
			{"HOST_NAME": "cn02", "STATUS": "unavail", "MAX": "16", "NJOBS": "0", "RUN": "0"}
		]
	}`

	res, err := parseLSFHostsOutput(jsonStr)
	if err != nil {
		t.Fatalf("parse lsf hosts: %v", err)
	}
	if res.Total != 2 {
		t.Fatalf("total = %d, want 2", res.Total)
	}
	if res.Online != 1 {
		t.Fatalf("online = %d, want 1", res.Online)
	}
	if res.Hosts[0].Name != "cn01" || !res.Hosts[0].IsOnline || res.Hosts[0].MaxSlots != 16 {
		t.Fatalf("host 0 mismatch: %+v", res.Hosts[0])
	}
}
