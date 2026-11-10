package scheduler

import (
	"cmp"
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

// unwrapSlurmValue recursively processes Slurm JSON values, replacing the
// {"set": bool, "infinite": bool, "number": N} wrapper objects with their
// numeric value directly. All other values pass through unchanged.
func unwrapSlurmValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		// Detect the Slurm {set, infinite, number} pattern (exactly these 3 keys)
		if len(val) == 3 {
			_, hasSet := val["set"]
			_, hasInfinite := val["infinite"]
			number, hasNumber := val["number"]
			if hasSet && hasInfinite && hasNumber {
				return number
			}
		}
		result := make(map[string]any, len(val))
		for k, v2 := range val {
			result[k] = unwrapSlurmValue(v2)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, v2 := range val {
			result[i] = unwrapSlurmValue(v2)
		}
		return result
	default:
		return v
	}
}

type slurmDriver struct {
	baseDriver
}

var reSlurmSubmit = regexp.MustCompile(`(?i)Submitted batch job (\d+)`)

// slurmJobDisplayFields are the columns recommended to clients through Meta.Fields.Job.
// Unlike LSF's `bjobs -o`, `squeue --json` cannot restrict its own
// output (Slurm ignores formatting arguments when dumping JSON), so this list is
// advisory only — slurmListRecordFields is what actually trims the payload.
// Order parallels LSF defaults: id, user, status, queue/partition, exec hosts, name, submit time.
// Resolve user/status via Meta.Keys (user→user_name, status→job_state).
var slurmJobDisplayFields = []string{
	"job_id", "user_name", "job_state", "partition", "nodes", "name", "submit_time",
}

// slurmHostDisplayFields: native sinfo --json node keys projected for list/meta.
var slurmHostDisplayFields = []string{
	"name", "hostname", "state", "cpus", "alloc_cpus", "idle_cpus",
	"real_memory", "alloc_memory", "cpu_load", "partitions", "reason", "weight",
}

// slurmQueueDisplayFields: native scontrol show partition --json keys.
// Slurm partitions do not expose LSF-style PEND/RUN counters; those live on jobs.
var slurmQueueDisplayFields = []string{
	"name", "priority_tier", "state", "total_nodes", "max_nodes",
	"max_cpus_per_node", "max_time", "nodes", "qos",
}

// slurmExtraListFields are kept on top of the display columns: the fields
// server-side aggregation needs plus the operational fields worth keeping.
// Every name was verified against the official Slurm job schema — the OpenAPI
// job response schema (slurm-22.05, ~108 keys) and the newer data_parser job
// definition (slurm-23.11 / master). group_name and failed_node only exist on
// newer Slurm; on older releases they are simply absent and get skipped.
var slurmExtraListFields = []string{
	// ownership / accounting
	"group_name", "account", "qos", "wckey",
	// state & scheduling
	"state_reason", "state_description", "cluster",
	"priority", "nice", "requeue", "restart_cnt", "resv_name", "failed_node",
	"dependency", "comment",
	// placement & resources
	"node_count", "cpus", "cpus_per_task", "batch_host",
	"tres_alloc_str", "tres_req_str", "memory_per_node", "memory_per_cpu",
	"features", "gres_detail",
	// timing
	"start_time", "end_time", "eligible_time", "suspend_time", "time_limit",
	// I/O & exit
	"command", "current_working_directory", "standard_input", "standard_output",
	"standard_error", "exit_code", "derived_exit_code",
	// arrays / heterogeneous jobs
	"array_job_id", "array_task_id", "array_task_string", "array_max_tasks", "het_job_id", "step_id",
	// submission & lifecycle
	"submit_line", "hold", "preempt_time", "pre_sus_time",
	// quota / licenses / classification
	"licenses", "licenses_allocated", "mcs_label", "job_size_str",
	// node & resource constraints
	"excluded_nodes", "required_nodes", "scheduled_nodes", "network",
	"cpus_per_tres", "memory_per_tres", "minimum_cpus_per_node",
	"minimum_tmp_disk_per_node",
	// identity / notes
	"user_id", "group_id", "system_comment",
}

// slurmListRecordFields is the field set kept on each list record. squeue --json
// dumps the whole job schema (~110-130 keys depending on release), so the list
// path trims it to this subset. It is derived from the display columns so that
// every advertised column is always renderable. All names are Slurm-native
// (taken verbatim from the official job schema); unknown keys are skipped, so
// this stays compatible with Slurm versions that lack a given field.
var slurmListRecordFields = append(slices.Clone(slurmJobDisplayFields), slurmExtraListFields...)

func newSlurmDriver() Scheduler {
	return &slurmDriver{
		jobFields:   append([]string(nil), slurmJobDisplayFields...),
		hostFields:  append([]string(nil), slurmHostDisplayFields...),
		queueFields: append([]string(nil), slurmQueueDisplayFields...),
	}
}

const (
	recentHistoryWindowStart = "now-2days"
)

func parseSlurmSubmitOutput(output string) (JobSubmitResult, error) {
	matches := reSlurmSubmit.FindStringSubmatch(output)
	if len(matches) < 2 {
		return JobSubmitResult{}, fmt.Errorf("failed to parse job ID from sbatch output: %s", output)
	}
	return JobSubmitResult{JobID: matches[1]}, nil
}

// --- High-level Execute Methods ---

func (d *slurmDriver) ExecuteListJobs(ctx context.Context, exec execution.Executor, query JobListQuery) (JobListResult, error) {
	args := []string{"--json"}
	if query.User == "" {
		// All users: omit --user; -a includes hidden/unavailable partitions.
		args = append(args, "-a")
	} else {
		args = append(args, "--user", query.User)
	}
	if query.Queue != "" {
		args = append(args, "--partition", query.Queue)
	}
	spec := payload.NewCommand("squeue", args...)

	output, err := runJobQuery(ctx, exec, spec, nil)
	if err != nil {
		return JobListResult{}, err
	}
	jobs, err := parseSlurmJobsJSONProjected(output, slurmListRecordFields)
	if err != nil {
		return JobListResult{}, err
	}
	return JobListResult{Jobs: jobs}, nil
}

func (d *slurmDriver) ExecuteListHosts(ctx context.Context, exec execution.Executor) (HostListResult, error) {
	spec := payload.NewCommand("sinfo", "--json")

	output, err := runJobQuery(ctx, exec, spec, nil)
	if err != nil {
		return HostListResult{}, err
	}
	return parseSlurmHostsJSON(output, d.hostFields)
}

func (d *slurmDriver) ExecuteListQueues(ctx context.Context, exec execution.Executor) (QueueListResult, error) {
	spec := payload.NewCommand("scontrol", "show", "partition", "--json")

	output, err := runJobQuery(ctx, exec, spec, nil)
	if err != nil {
		return QueueListResult{}, err
	}
	return parseSlurmQueuesJSON(output, d.queueFields)
}

func (d *slurmDriver) ExecuteGetJob(ctx context.Context, exec execution.Executor, jobID string) (map[string]any, error) {
	spec := payload.NewCommand("squeue", "--json", "--jobs", jobID)

	output, err := runJobQuery(ctx, exec, spec, nil)
	if err != nil {
		return nil, err
	}
	liveJobs, err := parseSlurmJobsJSON(output)
	if err != nil {
		return nil, err
	}
	if len(liveJobs) > 0 {
		return liveJobs[0], nil
	}

	// fall back to accounting history
	historySpec := payload.NewCommand("sacct",
		"--json", "--allocations",
		"--jobs", jobID,
		"--starttime", recentHistoryWindowStart,
	)

	historyOutput, err := runJobQuery(ctx, exec, historySpec, nil)
	if err != nil {
		return nil, err
	}
	historyJobs, err := parseSlurmJobsJSON(historyOutput)
	if err != nil {
		return nil, err
	}
	if len(historyJobs) == 0 {
		return nil, ErrJobNotFound
	}
	return historyJobs[0], nil
}

func (d *slurmDriver) ExecuteSubmit(ctx context.Context, exec execution.Executor, req JobSubmitRequest, targetNode string, nodeAffinity execution.NodeAffinity) (JobSubmitResult, error) {
	args := []string{fmt.Sprintf("--job-name=%s", req.Name)}
	if req.WorkDir != "" {
		args = append(args, fmt.Sprintf("--chdir=%s", req.WorkDir))
	}
	if req.LogDir != "" {
		args = append(args, fmt.Sprintf("--error=%s/%%j.err", req.LogDir), fmt.Sprintf("--output=%s/%%j.out", req.LogDir))
	}
	if req.Queue != "" {
		args = append(args, fmt.Sprintf("--partition=%s", req.Queue))
	}
	if len(req.Env) > 0 {
		envPairs := make([]string, 0, len(req.Env))
		for k, v := range req.Env {
			envPairs = append(envPairs, fmt.Sprintf("%s=%s", k, v))
		}
		args = append(args, fmt.Sprintf("--export=%s", strings.Join(envPairs, ",")))
	}
	args = append(args, fmt.Sprintf("--wrap=%s", req.Cmd))
	spec := payload.NewCommand("sbatch", args...)

	output, err := runJobSubmit(ctx, exec, spec, req.Env, req.User, targetNode, nodeAffinity)
	if err != nil {
		return JobSubmitResult{}, err
	}
	return parseSlurmSubmitOutput(output)
}

var slurmKeys = SchedulerKeyMap{
	ID:         "job_id",
	User:       "user_name",
	Status:     "job_state",
	SubmitTime: "submit_time",
}

// slurmActionStatuses 只列出 squeue --json 的基础状态（job_state 的第一个非空元素）。
// 其后的标志（POWER_UP_NODE、REQUEUE_HOLD 等）不参与比较。
// kill 含 CONFIGURING（节点已分配，Prolog/开机中，scancel 有效）和 RESIZING。
// COMPLETING / STAGE_OUT 已在收尾，不提供 kill。
// scontrol suspend 只接受 RUNNING；PENDING 与 CONFIGURING 会直接报错。
// Priority / Partition 更新只对尚未分配节点的 PENDING、REQUEUED 有效。
var slurmActionStatuses = map[JobOp][]string{
	JobOpKill:     {"PENDING", "REQUEUED", "CONFIGURING", "RUNNING", "RESIZING", "SUSPENDED"},
	JobOpSuspend:  {"RUNNING"},
	JobOpResume:   {"SUSPENDED"},
	JobOpToTop:    {"PENDING", "REQUEUED"},
	JobOpToBottom: {"PENDING", "REQUEUED"},
	JobOpSwitch:   {"PENDING", "REQUEUED"},
}

func (d *slurmDriver) Meta() SchedulerMeta {
	return SchedulerMeta{
		Fields: SchedulerDisplayFields{
			Job:   slices.Clone(d.jobFields),
			Host:  slices.Clone(d.hostFields),
			Queue: slices.Clone(d.queueFields),
		},
		Keys:    slurmKeys,
		Actions: cloneActionStatuses(slurmActionStatuses),
	}
}

func (d *slurmDriver) ExecuteControl(ctx context.Context, exec execution.Executor, jobID string, op JobOp, runUser string, args JobControlArgs) error {
	var jobCmd string
	var jobCmdArgs []string
	switch op {
	case JobOpKill:
		jobCmd = "scancel"
		jobCmdArgs = []string{jobID}
	case JobOpSuspend:
		jobCmd = "scontrol"
		jobCmdArgs = []string{"suspend", jobID}
	case JobOpResume:
		jobCmd = "scontrol"
		jobCmdArgs = []string{"resume", jobID}
	case JobOpToTop:
		jobCmd = "scontrol"
		jobCmdArgs = []string{"update", fmt.Sprintf("JobId=%s", jobID), "Priority=9999"}
	case JobOpToBottom:
		jobCmd = "scontrol"
		jobCmdArgs = []string{"update", fmt.Sprintf("JobId=%s", jobID), "Priority=1"}
	case JobOpSwitch:
		jobCmd = "scontrol"
		jobCmdArgs = []string{"update", fmt.Sprintf("JobId=%s", jobID), fmt.Sprintf("Partition=%s", args.TargetQueue)}
	default:
		return unsupportedJobControlOperation(op)
	}

	spec := payload.NewCommand(jobCmd, jobCmdArgs...)
	_, err := runJobControl(ctx, exec, spec, d.SysEnvs(), runUser)
	return err
}

func (d *slurmDriver) ExecuteModify(ctx context.Context, exec execution.Executor, jobID string, args JobModifyArgs, runUser string) error {
	updateArgs := []string{"update", fmt.Sprintf("JobId=%s", jobID)}
	if args.Queue != nil {
		updateArgs = append(updateArgs, fmt.Sprintf("Partition=%s", *args.Queue))
	}
	if args.Name != nil {
		updateArgs = append(updateArgs, fmt.Sprintf("Name=%s", *args.Name))
	}
	if len(updateArgs) == 2 {
		return fmt.Errorf("scontrol: at least one attribute must be specified")
	}
	spec := payload.NewCommand("scontrol", updateArgs...)
	_, err := runJobModify(ctx, exec, spec, runUser)
	return err
}

// parseSlurmJobsJSONProjected parses the JSON output of squeue or sacct, extracting only
// the requested projection fields and unwrapping only their values. This avoids recursive
// traversal of the ~85 unused Slurm fields on every job record.
func parseSlurmJobsJSONProjected(output string, keep []string) ([]map[string]any, error) {
	var resp struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse slurm JSON: %w", err)
	}
	jobs := make([]map[string]any, 0, len(resp.Jobs))
	for _, job := range resp.Jobs {
		out := make(map[string]any, len(keep))
		for _, k := range keep {
			if v, ok := job[k]; ok {
				out[k] = unwrapSlurmValue(v)
			}
		}
		jobs = append(jobs, out)
	}
	return jobs, nil
}

// parseSlurmJobsJSON parses the JSON output of squeue or sacct (both use
// {"jobs": [...]} as the top-level structure), unwrapping the Slurm-specific
// {set, infinite, number} wrapper objects into plain numeric values.
func parseSlurmJobsJSON(output string) ([]map[string]any, error) {
	var resp struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse slurm JSON: %w", err)
	}
	jobs := make([]map[string]any, 0, len(resp.Jobs))
	for _, job := range resp.Jobs {
		jobs = append(jobs, unwrapSlurmValue(job).(map[string]any))
	}
	return jobs, nil
}

var slurmStatusMap = map[string]NormalizedJobStatus{
	"RUNNING":       JobStatusRunning,
	"R":             JobStatusRunning,
	"CONFIGURING":   JobStatusRunning, // resources already allocated (prolog / power-up)
	"CF":            JobStatusRunning,
	"RESIZING":      JobStatusRunning,
	"RS":            JobStatusRunning,
	"COMPLETING":    JobStatusRunning,
	"CG":            JobStatusRunning,
	"PENDING":       JobStatusPending,
	"PD":            JobStatusPending,
	"SUSPENDED":     JobStatusSuspended,
	"S":             JobStatusSuspended,
	"REQUEUED":      JobStatusPending,
	"RQ":            JobStatusPending,
	"COMPLETED":     JobStatusCompleted,
	"CD":            JobStatusCompleted,
	"FAILED":        JobStatusFailed,
	"F":             JobStatusFailed,
	"CANCELLED":     JobStatusFailed,
	"CA":            JobStatusFailed,
	"TIMEOUT":       JobStatusFailed,
	"TO":            JobStatusFailed,
	"NODE_FAIL":     JobStatusFailed,
	"NF":            JobStatusFailed,
	"PREEMPTED":     JobStatusFailed,
	"PR":            JobStatusFailed,
	"BOOT_FAIL":     JobStatusFailed,
	"BF":            JobStatusFailed,
	"DEADLINE":      JobStatusFailed,
	"DL":            JobStatusFailed,
	"OUT_OF_MEMORY": JobStatusFailed,
	"OOM":           JobStatusFailed,
}

func (d *slurmDriver) NormalizeStatus(raw string) NormalizedJobStatus {
	upper := strings.ToUpper(strings.TrimSpace(raw))
	if st, ok := slurmStatusMap[upper]; ok {
		return st
	}
	return JobStatusUnknown
}

type slurmNodesJSONResponse struct {
	Nodes []map[string]any `json:"nodes"`
}

type slurmPartitionsJSONResponse struct {
	Partitions []map[string]any `json:"partitions"`
}

func parseSlurmHostsJSON(output string, keep []string) (HostListResult, error) {
	var resp slurmNodesJSONResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return HostListResult{}, fmt.Errorf("failed to parse sinfo json output: %w", err)
	}

	hosts := make([]map[string]any, 0, len(resp.Nodes))
	onlineCount := 0

	for _, rawNode := range resp.Nodes {
		node := unwrapSlurmValue(rawNode).(map[string]any)
		name, _ := node["name"].(string)
		hostName, _ := node["hostname"].(string)
		name = cmp.Or(name, hostName)
		if name == "" {
			continue
		}

		// Normalize state to a stable string for clients / online summary.
		if _, ok := node["state"]; ok {
			node["state"] = parseSlurmNodeState(node["state"])
		}
		if isSlurmHostOnline(node["state"]) {
			onlineCount++
		}
		hosts = append(hosts, projectJob(node, keep))
	}

	return HostListResult{
		Total:  len(hosts),
		Online: onlineCount,
		Hosts:  hosts,
	}, nil
}

func parseSlurmQueuesJSON(output string, keep []string) (QueueListResult, error) {
	var resp slurmPartitionsJSONResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return QueueListResult{}, fmt.Errorf("failed to parse scontrol partition json output: %w", err)
	}
	queues := make([]map[string]any, 0, len(resp.Partitions))
	for _, raw := range resp.Partitions {
		part := unwrapSlurmValue(raw).(map[string]any)
		name, _ := part["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		if _, ok := part["state"]; ok {
			part["state"] = parseSlurmNodeState(part["state"])
		}
		queues = append(queues, projectJob(part, keep))
	}
	slices.SortFunc(queues, func(a, b map[string]any) int {
		as, _ := a["name"].(string)
		bs, _ := b["name"].(string)
		return strings.Compare(as, bs)
	})
	return QueueListResult{Queues: queues}, nil
}

func isSlurmHostOnline(state any) bool {
	statusLower := strings.ToLower(parseSlurmNodeState(state))
	return !strings.Contains(statusLower, "down") &&
		!strings.Contains(statusLower, "drain") &&
		!strings.Contains(statusLower, "fail") &&
		!strings.Contains(statusLower, "maint") &&
		!strings.Contains(statusLower, "error")
}

func parseSlurmNodeState(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []any:
		parts := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ",")
	default:
		return "unknown"
	}
}
