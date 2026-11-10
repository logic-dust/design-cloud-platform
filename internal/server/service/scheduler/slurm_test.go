package scheduler

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

func TestSlurmExecuteSubmit(t *testing.T) {
	driver := newSlurmDriver()
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
	driver := newSlurmDriver()
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
	driver := newSlurmDriver()
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

	cmd := jobPayload.Command
	if cmd.Program != "scontrol" || !slices.Contains(cmd.Args, "Partition=new-partition") {
		t.Fatalf("command = %s %v, want scontrol ... Partition=new-partition", cmd.Program, cmd.Args)
	}
}

func TestSlurmExecuteModifyName(t *testing.T) {
	driver := newSlurmDriver()
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

	cmd := jobPayload.Command
	if cmd.Program != "scontrol" || !slices.Contains(cmd.Args, "Name=new-name") {
		t.Fatalf("command = %s %v, want scontrol ... Name=new-name", cmd.Program, cmd.Args)
	}
}

func TestSlurmExecuteModifyNoArgs(t *testing.T) {
	driver := newSlurmDriver()
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
	driver := newSlurmDriver()

	cases := []struct {
		raw  string
		want NormalizedJobStatus
	}{
		{"RUNNING", JobStatusRunning},
		{"running", JobStatusRunning},
		{"R", JobStatusRunning},
		{"CONFIGURING", JobStatusRunning},
		{"CF", JobStatusRunning},
		{"RESIZING", JobStatusRunning},
		{"RS", JobStatusRunning},
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

	res, err := parseSlurmHostsJSON(jsonStr, slurmHostDisplayFields)
	if err != nil {
		t.Fatalf("parse slurm hosts: %v", err)
	}
	if res.Total != 2 {
		t.Fatalf("total = %d, want 2", res.Total)
	}
	if res.Online != 1 {
		t.Fatalf("online = %d, want 1", res.Online)
	}
	if res.Hosts[0]["name"] != "node01" || res.Hosts[0]["state"] != "IDLE" {
		t.Fatalf("host 0 mismatch: %+v", res.Hosts[0])
	}
	if parseAnyInt(res.Hosts[0]["cpus"]) != 32 || parseAnyInt(res.Hosts[0]["alloc_cpus"]) != 8 {
		t.Fatalf("host 0 cpu mismatch: %+v", res.Hosts[0])
	}
}

func TestRawStatusString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   any
		want string
	}{
		{"plain string (LSF STAT)", "RUN", "RUN"},
		{"slurm single-element list", []any{"RUNNING"}, "RUNNING"},
		{"slurm list skips blank prefix", []any{"", "PENDING"}, "PENDING"},
		{"slurm base state before flags", []any{"CONFIGURING", "POWER_UP_NODE"}, "CONFIGURING"},
		{"empty list", []any{}, ""},
		{"missing field", nil, ""},
		{"unexpected type", 123, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rawStatusString(tc.in); got != tc.want {
				t.Errorf("rawStatusString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// squeue --json reports job_state as a list of strings (e.g. ["RUNNING"]),
// unlike LSF's plain-string STAT.
func TestSlurmJobStateFromSqueueJSON(t *testing.T) {
	t.Parallel()

	slurm := newSlurmDriver().(*slurmDriver)
	svc := &Service{ops: slurm}

	const squeueJSON = `{
		"jobs": [
			{"job_id": 1, "partition": "debug", "job_state": ["RUNNING"]},
			{"job_id": 2, "partition": "debug", "job_state": ["PENDING"]}
		]
	}`

	jobs, err := parseSlurmJobsJSON(squeueJSON)
	if err != nil {
		t.Fatalf("parse slurm jobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("len(jobs) = %d, want 2", len(jobs))
	}
	if got := svc.NormalizeJobStatus(jobs[0]); got != JobStatusRunning {
		t.Errorf("NormalizeJobStatus(job 1) = %q, want %q", got, JobStatusRunning)
	}
	if got := svc.NormalizeJobStatus(jobs[1]); got != JobStatusPending {
		t.Errorf("NormalizeJobStatus(job 2) = %q, want %q", got, JobStatusPending)
	}

	// Queues are no longer derived from job lists; status normalization still uses job_state lists.
	_ = jobs
}

// squeue --json dumps the full job schema; the list path must trim every record
// to slurmListRecordFields while keeping the fields clients and server-side
// aggregation depend on.
func TestSlurmListJobsProjectsRecordFields(t *testing.T) {
	t.Parallel()

	const squeueJSON = `{
		"jobs": [
			{
				"job_id": 42,
				"name": "train",
				"user_name": "alice",
				"job_state": ["RUNNING"],
				"partition": "gpu",
				"nodes": "node[01-02]",
				"submit_time": 1700000000,
				"command": "/home/alice/train.sh",
				"tres_alloc_str": "cpu=8,mem=16G,gres/gpu=1",
				"derived_exit_code": {"status": "SUCCESS", "return_code": 0},
				"job_resources": {"nodes": {"count": 2}},
				"steps": [{"name": "batch"}],
				"unknown_future_field": true
			}
		]
	}`

	jobs, err := parseSlurmJobsJSON(squeueJSON)
	if err != nil {
		t.Fatalf("parse slurm jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("len(jobs) = %d, want 1", len(jobs))
	}

	got := projectJob(jobs[0], slurmListRecordFields)
	for _, noisy := range []string{"job_resources", "steps", "unknown_future_field"} {
		if _, ok := got[noisy]; ok {
			t.Errorf("projected record still contains redundant key %q", noisy)
		}
	}
	for _, k := range []string{"job_id", "name", "user_name", "job_state", "partition", "command", "tres_alloc_str", "derived_exit_code"} {
		if _, ok := got[k]; !ok {
			t.Errorf("projected record dropped required key %q", k)
		}
	}
}

// Every advertised display column must survive list projection, otherwise
// clients would receive a Fields entry that no record can satisfy.
func TestSlurmDisplayFieldsKeptByProjection(t *testing.T) {
	t.Parallel()

	kept := make(map[string]struct{}, len(slurmListRecordFields))
	for _, k := range slurmListRecordFields {
		kept[k] = struct{}{}
	}
	for _, k := range slurmJobDisplayFields {
		if _, ok := kept[k]; !ok {
			t.Errorf("display field %q is not retained by slurmListRecordFields", k)
		}
	}
}

// The list path trims squeue's full dump, but the detail path must stay
// full-fidelity so clients can still drill into a single job.
func TestSlurmExecuteListJobsUsesAllPartitions(t *testing.T) {
	t.Parallel()
	driver := newSlurmDriver()
	exec := &mockExecutor{resp: &execution.ExecuteResponse{Success: true, Stdout: `{"jobs":[]}`}}

	_, err := driver.ExecuteListJobs(context.Background(), exec, JobListQuery{})
	if err != nil {
		t.Fatalf("ExecuteListJobs: %v", err)
	}
	jobPayload, ok := exec.lastReq.Payload.(payload.JobQuery)
	if !ok {
		t.Fatalf("payload type %T", exec.lastReq.Payload)
	}
	args := jobPayload.Command.Args
	if !slices.Contains(args, "-a") {
		t.Fatalf("cluster-wide list should pass -a, got %v", args)
	}
	if slices.Contains(args, "--user") {
		t.Fatalf("cluster-wide list should not pass --user, got %v", args)
	}
}

func TestSlurmExecuteListJobsTrimsRecords(t *testing.T) {
	t.Parallel()

	const squeueJSON = `{"jobs":[{"job_id":7,"name":"j","user_name":"bob","job_state":["PENDING"],"partition":"gpu","tres_alloc_str":"cpu=1","job_resources":{"nodes":{"count":1}}}]}`
	exec := &mockExecutor{resp: &execution.ExecuteResponse{Success: true, Stdout: squeueJSON}}
	driver := newSlurmDriver()

	res, err := driver.ExecuteListJobs(context.Background(), exec, JobListQuery{})
	if err != nil {
		t.Fatalf("ExecuteListJobs: %v", err)
	}
	if len(driver.Meta().Fields.Job) == 0 {
		t.Error("Meta().Fields.Job is empty")
	}
	if len(res.Jobs) != 1 {
		t.Fatalf("len(Jobs) = %d, want 1", len(res.Jobs))
	}
	if _, ok := res.Jobs[0]["job_resources"]; ok {
		t.Errorf("list record was not trimmed: %v", res.Jobs[0])
	}
	if _, ok := res.Jobs[0]["job_state"]; !ok {
		t.Error("list record dropped job_state")
	}

	detail, err := driver.ExecuteGetJob(context.Background(), exec, "7")
	if err != nil {
		t.Fatalf("ExecuteGetJob: %v", err)
	}
	if _, ok := detail["job_resources"]; !ok {
		t.Errorf("detail record should keep full fields: %v", detail)
	}
}

func TestSlurmMetaAntiDrift(t *testing.T) {
	t.Parallel()
	driver := newSlurmDriver()
	meta := driver.Meta()

	if len(meta.Fields.Job) == 0 {
		t.Fatal("Meta().Fields.Job is empty")
	}

	kept := make(map[string]struct{}, len(slurmListRecordFields))
	for _, k := range slurmListRecordFields {
		kept[k] = struct{}{}
	}
	for _, f := range meta.Fields.Job {
		if _, ok := kept[f]; !ok {
			t.Errorf("field %q is not in slurmListRecordFields", f)
		}
	}
	if len(meta.Fields.Host) == 0 || len(meta.Fields.Queue) == 0 {
		t.Fatalf("Fields.Host/Queue empty: %+v", meta.Fields)
	}

	if meta.Keys.Status != "job_state" {
		t.Errorf("Keys.Status = %q, want job_state", meta.Keys.Status)
	}
	for name, key := range map[string]string{
		"ID":         meta.Keys.ID,
		"User":       meta.Keys.User,
		"Status":     meta.Keys.Status,
		"SubmitTime": meta.Keys.SubmitTime,
	} {
		if key == "" {
			t.Errorf("Keys.%s is empty", name)
		}
		if !slices.Contains(meta.Fields.Job, key) {
			t.Errorf("Keys.%s (%q) is not in Fields.Job", name, key)
		}
	}

	expectedActions := map[JobOp][]string{
		JobOpKill:     {"PENDING", "REQUEUED", "CONFIGURING", "RUNNING", "RESIZING", "SUSPENDED"},
		JobOpSuspend:  {"RUNNING"},
		JobOpResume:   {"SUSPENDED"},
		JobOpToTop:    {"PENDING", "REQUEUED"},
		JobOpToBottom: {"PENDING", "REQUEUED"},
		JobOpSwitch:   {"PENDING", "REQUEUED"},
	}
	if !maps.EqualFunc(meta.Actions, expectedActions, slices.Equal) {
		t.Fatalf("Actions = %#v, want %#v", meta.Actions, expectedActions)
	}

	meta.Actions[JobOpKill] = append(meta.Actions[JobOpKill], "COMPLETED")
	meta.Fields.Job[0] = "MUTATED"
	again := driver.Meta()
	if slices.Contains(again.Actions[JobOpKill], "COMPLETED") || again.Fields.Job[0] == "MUTATED" {
		t.Fatal("Meta() returned aliases of the driver tables")
	}

	exec := &mockExecutor{resp: &execution.ExecuteResponse{Success: true}}
	for action, statuses := range expectedActions {
		if len(statuses) == 0 {
			t.Errorf("action %q has empty statuses", action)
			continue
		}
		seen := make(map[string]struct{})
		for _, st := range statuses {
			if _, dup := seen[st]; dup {
				t.Errorf("action %q has duplicate status %q", action, st)
			}
			seen[st] = struct{}{}

			norm := driver.NormalizeStatus(st)
			if norm != JobStatusRunning && norm != JobStatusPending && norm != JobStatusSuspended {
				t.Errorf("action %q status %q normalized to non-active status %q", action, st, norm)
			}
		}

		err := driver.ExecuteControl(context.Background(), exec, "123", action, "alice", JobControlArgs{TargetQueue: "normal"})
		if errors.Is(err, ErrUnsupportedJobControlOperation) {
			t.Errorf("action %q returned ErrUnsupportedJobControlOperation", action)
		}
	}
}
