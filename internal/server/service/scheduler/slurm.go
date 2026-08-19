package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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

func newSlurmDriver(envScript string) Scheduler {
	return &slurmDriver{
		envScript: envScript,
		jobFields: []string{
			"job_id", "name", "user_name", "partition", "job_state", "submit_time", "nodes", "state_reason",
		},
	}
}

func (d *slurmDriver) StatusKey() string { return "job_state" }

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
	if query.User != "" {
		args = append(args, "--user", query.User)
	}
	if query.Queue != "" {
		args = append(args, "--partition", query.Queue)
	}
	spec := payload.NewCommand("squeue", args...)

	output, err := runListJobsQuery(ctx, exec, spec, nil, d.envScript, query)
	if err != nil {
		return JobListResult{}, err
	}
	jobs, err := parseSlurmJobsJSON(output)
	if err != nil {
		return JobListResult{}, err
	}
	return JobListResult{Fields: d.JobFields(), Jobs: jobs}, nil
}

func (d *slurmDriver) ExecuteListHosts(ctx context.Context, exec execution.Executor) (HostListResult, error) {
	spec := payload.NewCommand("sinfo", "--json")

	output, err := runListHostsQuery(ctx, exec, spec, nil, d.envScript)
	if err != nil {
		return HostListResult{}, err
	}
	return parseSlurmHostsJSON(output)
}

func (d *slurmDriver) ExecuteListQueues(_ context.Context, _ execution.Executor, jobs []map[string]any) (QueueListResult, error) {
	return d.aggregateQueuesFromJobs(jobs, []string{"partition", "PARTITION"}, d.StatusKey(), d.NormalizeStatus), nil
}

func (d *slurmDriver) ExecuteGetJob(ctx context.Context, exec execution.Executor, jobID string) (map[string]any, error) {
	spec := payload.NewCommand("squeue", "--json", "--jobs", jobID)

	output, err := runGetJobQuery(ctx, exec, spec, nil, d.envScript, jobID)
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

	historyOutput, err := runGetJobQuery(ctx, exec, historySpec, nil, d.envScript, jobID)
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

	output, err := runJobSubmit(ctx, exec, spec, req, d.envScript, targetNode, nodeAffinity)
	if err != nil {
		return JobSubmitResult{}, err
	}
	return parseSlurmSubmitOutput(output)
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
	_, err := runJobControl(ctx, exec, spec, d.SysEnvs(), d.envScript, jobID, op, runUser, "", args.TargetQueue)
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
	_, err := runJobModify(ctx, exec, spec, d.envScript, jobID, args, runUser)
	return err
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

func parseSlurmHostsJSON(output string) (HostListResult, error) {
	var resp slurmNodesJSONResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return HostListResult{}, fmt.Errorf("failed to parse sinfo json output: %w", err)
	}

	hosts := make([]HostStats, 0, len(resp.Nodes))
	onlineCount := 0

	for _, rawNode := range resp.Nodes {
		node := unwrapSlurmValue(rawNode).(map[string]any)
		name, _ := node["name"].(string)
		if name == "" {
			name, _ = node["hostname"].(string)
		}
		if name == "" {
			continue
		}

		statusStr := parseSlurmNodeState(node["state"])
		statusLower := strings.ToLower(statusStr)

		isOnline := !strings.Contains(statusLower, "down") &&
			!strings.Contains(statusLower, "drain") &&
			!strings.Contains(statusLower, "fail") &&
			!strings.Contains(statusLower, "maint") &&
			!strings.Contains(statusLower, "error")

		if isOnline {
			onlineCount++
		}

		maxCpus := parseAnyInt(node["cpus"])
		allocCpus := parseAnyInt(node["alloc_cpus"])

		hosts = append(hosts, HostStats{
			Name:      name,
			Status:    statusStr,
			IsOnline:  isOnline,
			MaxSlots:  maxCpus,
			UsedSlots: allocCpus,
		})
	}

	return HostListResult{
		Total:  len(hosts),
		Online: onlineCount,
		Hosts:  hosts,
	}, nil
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
