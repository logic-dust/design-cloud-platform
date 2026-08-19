package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

type lsfDriver struct {
	baseDriver
}

var reLSFSubmit = regexp.MustCompile(`Job <(\d+)> is submitted to (?:default )?queue <([^>]+)>`)

func newLSFDriver(envScript string) Scheduler {
	return &lsfDriver{
		envScript: envScript,
		sysEnvs: map[string]string{
			"LSB_SHORT_HOSTLIST":  "1",
			"LSB_DISPLAY_YEAR":    "Y",
			"LSB_HMS_TIME_FORMAT": "Y",
		},
		jobFields: []string{
			"JOBID", "USER", "STAT", "QUEUE", "FROM_HOST", "EXEC_HOST", "JOB_NAME", "SUBMIT_TIME", "RUN_TIME",
		},
	}
}

func (d *lsfDriver) StatusKey() string { return "STAT" }

const (
	// LSB_BJOBS_FORMAT
	bjobsOutputFields = "JOBID USER STAT QUEUE FROM_HOST EXEC_HOST JOB_NAME SUBMIT_TIME RUN_TIME EXEC_CWD OUTPUT_DIR INPUT_FILE OUTPUT_FILE ERROR_FILE EXIT_CODE EXIT_REASON"
)

type bjobsJSONResponse struct {
	Jobs    int              `json:"JOBS"`
	Records []map[string]any `json:"RECORDS"`
}

func (d *lsfDriver) parseGetJobsOutput(output string) (JobListResult, error) {
	var result bjobsJSONResponse
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return JobListResult{}, fmt.Errorf("failed to parse bjobs json output: %w", err)
	}
	// lsf输出json格式例子：
	//{
	//	"COMMAND":"bjobs",
	//	"JOBS":1,
	//	"RECORDS":[
	//	  {
	//		"JOBID":"123",
	//		"ERROR":"Job <123> is not found" //找不到时这个字段下面就没有内容了
	//      ...其他字段，如果没有ERROR字段，则后面就是其给给定的要查询的字段
	//	  }
	//	]
	//}

	return JobListResult{
		Fields: d.JobFields(),
		Jobs:   result.Records,
	}, nil
}

func parseLSFSubmitOutput(output string) (JobSubmitResult, error) {
	matches := reLSFSubmit.FindStringSubmatch(output)
	if len(matches) < 3 {
		return JobSubmitResult{}, fmt.Errorf("failed to parse bsub output: %s", output)
	}

	return JobSubmitResult{
		JobID: matches[1],
		Queue: matches[2],
	}, nil
}

// --- High-level Execute Methods ---

func (d *lsfDriver) ExecuteListJobs(ctx context.Context, exec execution.Executor, query JobListQuery) (JobListResult, error) {
	args := []string{"-json", "-o", bjobsOutputFields, "-a"}
	if query.User != "" {
		args = append(args, "-u", query.User)
	} else {
		args = append(args, "-u", "all")
	}
	if query.Queue != "" {
		args = append(args, "-q", query.Queue)
	}
	if query.Node != "" {
		args = append(args, "-m", query.Node)
	}

	spec := payload.NewCommand("bjobs", args...)

	output, err := runListJobsQuery(ctx, exec, spec, d.SysEnvs(), d.envScript, query)
	if err != nil {
		return JobListResult{}, err
	}
	return d.parseGetJobsOutput(output)
}

func (d *lsfDriver) ExecuteListHosts(ctx context.Context, exec execution.Executor) (HostListResult, error) {
	spec := payload.NewCommand("bhosts", "-json", "-o", bhostsOutputFields)

	output, err := runListHostsQuery(ctx, exec, spec, d.SysEnvs(), d.envScript)
	if err != nil {
		return HostListResult{}, err
	}
	return parseLSFHostsOutput(output)
}

func (d *lsfDriver) ExecuteListQueues(_ context.Context, _ execution.Executor, jobs []map[string]any) (QueueListResult, error) {
	return d.aggregateQueuesFromJobs(jobs, []string{"QUEUE", "queue"}, d.StatusKey(), d.NormalizeStatus), nil
}

func (d *lsfDriver) ExecuteGetJob(ctx context.Context, exec execution.Executor, jobID string) (map[string]any, error) {
	spec := payload.NewCommand("bjobs", "-json", "-o", bjobsOutputFields, jobID)

	output, err := runGetJobQuery(ctx, exec, spec, d.SysEnvs(), d.envScript, jobID)
	if err != nil {
		return nil, err
	}

	result, err := d.parseGetJobsOutput(output)
	if err != nil {
		return nil, err
	}
	if len(result.Jobs) == 0 {
		return nil, ErrJobNotFound
	}
	if errVal, _ := result.Jobs[0]["ERROR"].(string); errVal != "" {
		return nil, errors.New(errVal)
	}

	return result.Jobs[0], nil
}

func (d *lsfDriver) ExecuteSubmit(ctx context.Context, exec execution.Executor, req JobSubmitRequest, targetNode string, nodeAffinity execution.NodeAffinity) (JobSubmitResult, error) {
	args := []string{"-J", req.Name}
	if req.WorkDir != "" {
		args = append(args, "-cwd", req.WorkDir)
	}
	if req.Queue != "" {
		args = append(args, "-q", req.Queue)
	}
	if req.LogDir != "" {
		args = append(args, "-e", fmt.Sprintf("%s/%%J.err", req.LogDir), "-o", fmt.Sprintf("%s/%%J.out", req.LogDir))
	}
	args = append(args, req.Cmd)
	spec := payload.NewCommand("bsub", args...)

	output, err := runJobSubmit(ctx, exec, spec, req, d.envScript, targetNode, nodeAffinity)
	if err != nil {
		return JobSubmitResult{}, err
	}
	return parseLSFSubmitOutput(output)
}

func (d *lsfDriver) ExecuteControl(ctx context.Context, exec execution.Executor, jobID string, op JobOp, runUser string, args JobControlArgs) error {
	var jobCmd string
	var jobCmdArgs []string
	switch op {
	case JobOpKill:
		jobCmd = "bkill"
	case JobOpSuspend:
		jobCmd = "bstop"
	case JobOpResume:
		jobCmd = "bresume"
	case JobOpToTop:
		jobCmd = "btop"
	case JobOpToBottom:
		jobCmd = "bbot"
	case JobOpSwitch:
		jobCmd = "bswitch"
		jobCmdArgs = []string{args.TargetQueue}
	default:
		return unsupportedJobControlOperation(op)
	}

	jobCmdArgs = append(jobCmdArgs, jobID)
	spec := payload.NewCommand(jobCmd, jobCmdArgs...)
	_, err := runJobControl(ctx, exec, spec, d.SysEnvs(), d.envScript, jobID, op, runUser, "", args.TargetQueue)
	return err
}

func (d *lsfDriver) ExecuteModify(ctx context.Context, exec execution.Executor, jobID string, args JobModifyArgs, runUser string) error {
	var cmdArgs []string
	if args.Queue != nil {
		cmdArgs = append(cmdArgs, "-q", *args.Queue)
	}
	if args.Name != nil {
		cmdArgs = append(cmdArgs, "-n", *args.Name)
	}
	if len(cmdArgs) == 0 {
		return fmt.Errorf("bmod: at least one attribute must be specified")
	}
	cmdArgs = append(cmdArgs, jobID)
	spec := payload.NewCommand("bmod", cmdArgs...)
	_, err := runJobModify(ctx, exec, spec, d.envScript, jobID, args, runUser)
	return err
}

var lsfStatusMap = map[string]NormalizedJobStatus{
	"RUN":   JobStatusRunning,
	"PEND":  JobStatusPending,
	"PSUSP": JobStatusSuspended,
	"SSUSP": JobStatusSuspended,
	"USUSP": JobStatusSuspended,
	"WAIT":  JobStatusPending,
	"DONE":  JobStatusCompleted,
	"EXIT":  JobStatusFailed,
	"UNKWN": JobStatusFailed,
	"ZOMBI": JobStatusFailed,
}

func (d *lsfDriver) NormalizeStatus(raw string) NormalizedJobStatus {
	upper := strings.ToUpper(strings.TrimSpace(raw))
	if st, ok := lsfStatusMap[upper]; ok {
		return st
	}
	return JobStatusUnknown
}

const bhostsOutputFields = "HOST_NAME STATUS MAX NJOBS RUN SSUSP USUSP RSV"

type bhostsJSONResponse struct {
	Hosts   int              `json:"HOSTS"`
	Records []map[string]any `json:"RECORDS"`
}

func parseLSFHostsOutput(output string) (HostListResult, error) {
	var result bhostsJSONResponse
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return HostListResult{}, fmt.Errorf("failed to parse bhosts json output: %w", err)
	}

	hosts := make([]HostStats, 0, len(result.Records))
	onlineCount := 0

	for _, rec := range result.Records {
		name, _ := rec["HOST_NAME"].(string)
		if name == "" {
			continue
		}
		status, _ := rec["STATUS"].(string)
		statusLower := strings.ToLower(strings.TrimSpace(status))

		isOnline := statusLower == "ok" || statusLower == "closed" || strings.HasPrefix(statusLower, "ok") || strings.HasPrefix(statusLower, "closed")
		if isOnline {
			onlineCount++
		}

		maxVal := parseAnyInt(rec["MAX"])
		njobsVal := parseAnyInt(rec["NJOBS"])
		runVal := parseAnyInt(rec["RUN"])

		hosts = append(hosts, HostStats{
			Name:      name,
			Status:    status,
			IsOnline:  isOnline,
			MaxSlots:  maxVal,
			UsedSlots: njobsVal,
			RunJobs:   runVal,
		})
	}

	return HostListResult{
		Total:  len(hosts),
		Online: onlineCount,
		Hosts:  hosts,
	}, nil
}

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
