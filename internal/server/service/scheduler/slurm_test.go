package scheduler

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

func TestSlurmExecuteSubmit(t *testing.T) {
	driver := newSlurmDriver("/path/to/profile.slurm")
	exec := &mockExecutor{
		resp: &execution.ExecuteResponse{Success: true, Stdout: "Submitted batch job 123"},
	}

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
	expectedArg := fmt.Sprintf("--partition=%s", req.Queue)
	if slices.Contains(cmd.Args, expectedArg) {
		found = true
	}
	if !found {
		t.Fatalf("Command args do not contain %s: %v", expectedArg, cmd.Args)
	}
}

func TestSlurmExecuteSubmitEmptyQueue(t *testing.T) {
	driver := newSlurmDriver("/path/to/profile.slurm")
	exec := &mockExecutor{
		resp: &execution.ExecuteResponse{Success: true, Stdout: "Submitted batch job 123"},
	}

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
		if strings.HasPrefix(arg, "--partition=") {
			t.Fatalf("Command args contain --partition when queue is empty: %v", cmd.Args)
		}
	}
}

func TestSlurmExecuteModifyQueue(t *testing.T) {
	driver := newSlurmDriver("/path/to/profile.slurm")
	exec := &mockExecutor{
		resp: &execution.ExecuteResponse{Success: true, Stdout: ""},
	}

	args := JobModifyArgs{Queue: new("new-partition")}
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

	if jobPayload.Queue == nil || *jobPayload.Queue != "new-partition" {
		t.Fatalf("Payload.Queue = %v, want 'new-partition'", jobPayload.Queue)
	}

	cmd := jobPayload.Command
	if cmd.Program != "scontrol" {
		t.Fatalf("Program = %s, want scontrol", cmd.Program)
	}
}

func TestSlurmExecuteModifyName(t *testing.T) {
	driver := newSlurmDriver("/path/to/profile.slurm")
	exec := &mockExecutor{
		resp: &execution.ExecuteResponse{Success: true, Stdout: ""},
	}

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
	if cmd.Program != "scontrol" {
		t.Fatalf("Program = %s, want scontrol", cmd.Program)
	}
}

func TestSlurmExecuteModifyNoArgs(t *testing.T) {
	driver := newSlurmDriver("/path/to/profile.slurm")
	exec := &mockExecutor{
		resp: &execution.ExecuteResponse{Success: true, Stdout: ""},
	}

	args := JobModifyArgs{}
	err := driver.ExecuteModify(context.Background(), exec, "123", args, "testuser")
	if err == nil {
		t.Fatal("ExecuteModify should fail with no args")
	}
}

func TestSlurmNormalizeStatus(t *testing.T) {
	t.Parallel()
	driver := newSlurmDriver("")

	cases := []struct {
		raw  string
		want NormalizedJobStatus
	}{
		{"RUNNING", JobStatusRunning},
		{"running", JobStatusRunning},
		{"R", JobStatusRunning},
		{"COMPLETING", JobStatusRunning},
		{"CG", JobStatusRunning},
		{"PENDING", JobStatusPending},
		{"PD", JobStatusPending},
		{"REQUEUED", JobStatusPending},
		{"RQ", JobStatusPending},
		{"SUSPENDED", JobStatusSuspended},
		{"S", JobStatusSuspended},
		{"COMPLETED", JobStatusCompleted},
		{"CD", JobStatusCompleted},
		{"FAILED", JobStatusFailed},
		{"F", JobStatusFailed},
		{"CANCELLED", JobStatusFailed},
		{"CA", JobStatusFailed},
		{"TIMEOUT", JobStatusFailed},
		{"TO", JobStatusFailed},
		{"NODE_FAIL", JobStatusFailed},
		{"NF", JobStatusFailed},
		{"PREEMPTED", JobStatusFailed},
		{"PR", JobStatusFailed},
		{"BOOT_FAIL", JobStatusFailed},
		{"BF", JobStatusFailed},
		{"DEADLINE", JobStatusFailed},
		{"DL", JobStatusFailed},
		{"OUT_OF_MEMORY", JobStatusFailed},
		{"OOM", JobStatusFailed},
		// Unknown statuses must return JobStatusUnknown, not be guessed.
		{"RUN", JobStatusUnknown},
		{"PEND", JobStatusUnknown},
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

func TestParseSlurmHostsJSON(t *testing.T) {
	t.Parallel()

	jsonStr := `{
		"nodes": [
			{"name": "node01", "state": ["IDLE"], "cpus": {"set": true, "infinite": false, "number": 32}, "alloc_cpus": {"set": true, "infinite": false, "number": 8}},
			{"name": "node02", "state": ["DOWN"], "cpus": {"set": true, "infinite": false, "number": 32}, "alloc_cpus": {"set": true, "infinite": false, "number": 0}}
		]
	}`

	res, err := parseSlurmHostsJSON(jsonStr)
	if err != nil {
		t.Fatalf("parse slurm hosts: %v", err)
	}
	if res.Total != 2 {
		t.Fatalf("total = %d, want 2", res.Total)
	}
	if res.Online != 1 {
		t.Fatalf("online = %d, want 1", res.Online)
	}
	if res.Hosts[0].Name != "node01" || !res.Hosts[0].IsOnline || res.Hosts[0].MaxSlots != 32 || res.Hosts[0].UsedSlots != 8 {
		t.Fatalf("host 0 mismatch: %+v", res.Hosts[0])
	}
}
