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

type mockExecutor struct {
	lastReq *execution.ExecuteRequest
	resp    *execution.ExecuteResponse
	err     error
}

// parseAnyInt 把 JSON/命令输出里的数值统一成 int，供 LSF/Slurm 断言使用。
func parseAnyInt(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case string:
		var n int
		_, _ = fmt.Sscanf(strings.TrimSpace(val), "%d", &n)
		return n
	default:
		return 0
	}
}

func TestLSFExecuteGetJobClassifiesNotFound(t *testing.T) {
	t.Parallel()
	driver := newLSFDriver()
	exec := &mockExecutor{resp: &execution.ExecuteResponse{
		Success: true,
		Stdout:  `{"JOBS":1,"RECORDS":[{"JOBID":"123","ERROR":"Job <123> is not found"}]}`,
	}}

	_, err := driver.ExecuteGetJob(context.Background(), exec, "123")
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("ExecuteGetJob error = %v, want ErrJobNotFound", err)
	}
}

func (m *mockExecutor) Execute(ctx context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	m.lastReq = req
	if m.resp != nil {
		return m.resp, m.err
	}
	return &execution.ExecuteResponse{Success: true, Stdout: "Job <123> is submitted to queue <normal>"}, m.err
}

func TestLSFExecuteSubmit(t *testing.T) {
	driver := newLSFDriver()
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
	driver := newLSFDriver()
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
	driver := newLSFDriver()
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

	cmd := jobPayload.Command
	if cmd.Program != "bmod" || !slices.Contains(cmd.Args, "new-queue") {
		t.Fatalf("command = %s %v, want bmod ... new-queue", cmd.Program, cmd.Args)
	}
}

func TestLSFExecuteModifyName(t *testing.T) {
	driver := newLSFDriver()
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

	cmd := jobPayload.Command
	if cmd.Program != "bmod" || !slices.Contains(cmd.Args, "new-name") {
		t.Fatalf("command = %s %v, want bmod ... new-name", cmd.Program, cmd.Args)
	}
}

func TestLSFExecuteModifyNoArgs(t *testing.T) {
	driver := newLSFDriver()
	exec := &mockExecutor{}

	args := JobModifyArgs{}
	err := driver.ExecuteModify(context.Background(), exec, "123", args, "testuser")
	if err == nil {
		t.Fatal("ExecuteModify should fail with no args")
	}
}

func TestLSFExecuteListJobs(t *testing.T) {
	driver := newLSFDriver()
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
	driver := newLSFDriver()

	cases := []struct {
		raw  string
		want NormalizedJobStatus
	}{
		{"RUN", JobStatusRunning},
		{"run", JobStatusRunning},
		{"PROV", JobStatusRunning},
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
	name, _ := res.Hosts[0]["HOST_NAME"].(string)
	status, _ := res.Hosts[0]["STATUS"].(string)
	if name != "cn01" || status != "ok" || !isLSFHostOnline(status) {
		t.Fatalf("host 0 mismatch: %+v", res.Hosts[0])
	}
	if parseAnyInt(res.Hosts[0]["MAX"]) != 16 {
		t.Fatalf("host 0 MAX = %v, want 16", res.Hosts[0]["MAX"])
	}
}

func TestParseLSFQueuesOutput(t *testing.T) {
	t.Parallel()
	jsonStr := `{
		"COMMAND": "bqueues",
		"QUEUES": 1,
		"RECORDS": [
			{"QUEUE_NAME": "normal", "PRIORITY": "30", "STATUS": "Open:Active", "NJOBS": "2", "PEND": "1", "RUN": "1", "SUSP": "0"}
		]
	}`
	res, err := parseLSFQueuesOutput(jsonStr)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Queues) != 1 {
		t.Fatalf("len = %d", len(res.Queues))
	}
	if res.Queues[0]["QUEUE_NAME"] != "normal" || res.Queues[0]["PRIORITY"] != "30" {
		t.Fatalf("queue mismatch: %+v", res.Queues[0])
	}
}

// The display columns stay stable while bjobs -o fetches a wider record set.
// Field names must exist in LSF (verified against LSF 10.1: an unknown name
// makes bjobs fail outright) and the display columns must all be fetched.
func TestLSFJobFieldsSuperset(t *testing.T) {
	t.Parallel()

	display := newLSFDriver().Meta().Fields.Job
	if len(display) == 0 {
		t.Fatal("Meta().Fields.Job is empty")
	}

	fetched := make(map[string]struct{})
	for f := range strings.FieldsSeq(bjobsOutputFields) {
		fetched[f] = struct{}{}
	}
	for _, f := range display {
		if _, ok := fetched[f]; !ok {
			t.Errorf("display field %q is not fetched by bjobsOutputFields", f)
		}
	}
	for _, f := range []string{"START_TIME", "PEND_TIME", "CPU_USED", "MEM", "SLOTS", "PROJ_NAME"} {
		if _, ok := fetched[f]; !ok {
			t.Errorf("bjobsOutputFields is missing %q", f)
		}
	}
}

func TestLSFMetaAntiDrift(t *testing.T) {
	t.Parallel()
	driver := newLSFDriver()
	meta := driver.Meta()

	if len(meta.Fields.Job) == 0 {
		t.Fatal("Meta().Fields.Job is empty")
	}

	fetched := make(map[string]struct{})
	for f := range strings.FieldsSeq(bjobsOutputFields) {
		fetched[f] = struct{}{}
	}
	for _, f := range meta.Fields.Job {
		if _, ok := fetched[f]; !ok {
			t.Errorf("field %q is not in bjobsOutputFields", f)
		}
	}

	wantOrder := []string{"JOBID", "USER", "STAT", "QUEUE", "FROM_HOST", "EXEC_HOST", "JOB_NAME", "SUBMIT_TIME"}
	if !slices.Equal(meta.Fields.Job, wantOrder) {
		t.Errorf("Fields.Job order = %v, want %v", meta.Fields.Job, wantOrder)
	}
	if meta.Keys.Status != "STAT" || meta.Keys.User != "USER" {
		t.Errorf("Keys = %+v, want USER/STAT", meta.Keys)
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
	if !slices.Equal(meta.Fields.Host, strings.Fields(hostFields)) {
		t.Errorf("Fields.Host = %v, want %v", meta.Fields.Host, strings.Fields(hostFields))
	}
	if !slices.Equal(meta.Fields.Queue, strings.Fields(queueFields)) {
		t.Errorf("Fields.Queue = %v, want %v", meta.Fields.Queue, strings.Fields(queueFields))
	}

	expectedActions := map[JobOp][]string{
		JobOpKill:     {"PEND", "RUN", "PROV", "PSUSP", "USUSP", "SSUSP", "WAIT"},
		JobOpSuspend:  {"PEND", "RUN"},
		JobOpResume:   {"PSUSP", "USUSP"},
		JobOpToTop:    {"PEND"},
		JobOpToBottom: {"PEND"},
		JobOpSwitch:   {"PEND"},
	}
	if !maps.EqualFunc(meta.Actions, expectedActions, slices.Equal) {
		t.Fatalf("Actions = %#v, want %#v", meta.Actions, expectedActions)
	}

	meta.Actions[JobOpKill] = append(meta.Actions[JobOpKill], "DONE")
	meta.Fields.Job[0] = "MUTATED"
	again := driver.Meta()
	if slices.Contains(again.Actions[JobOpKill], "DONE") || again.Fields.Job[0] == "MUTATED" {
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
