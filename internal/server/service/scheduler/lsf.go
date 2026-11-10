package scheduler

import (
	"cmp"
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

type lsfDriver struct {
	baseDriver
}

var reLSFSubmit = regexp.MustCompile(`Job <(\d+)> is submitted to (?:default )?queue <([^>]+)>`)

// jobFields 是建议客户端展示的作业列（Meta.Fields.Job）。顺序对齐常见 bjobs 默认观感：
// JOBID USER STAT QUEUE FROM_HOST EXEC_HOST JOB_NAME SUBMIT_TIME。
// 用户/状态列请用 Meta.Keys（user→USER、status→STAT），不要假定固定下标。
const jobFields = "JOBID USER STAT QUEUE FROM_HOST EXEC_HOST JOB_NAME SUBMIT_TIME"

// hostFields / queueFields 为 JSON 响应中的原生键名（LSF -o 里 JL/U 写作 JL_U；
// PRIO 在 JSON 中落地为 PRIORITY）。已在 LSF 10.1 实机验证。
const hostFields = "HOST_NAME STATUS JL_U MAX NJOBS RUN SSUSP USUSP RSV"

// queueFields：Meta.Fields.Queue 推荐列序；限额列 MAX / JL_* 置末（JL/U→JSON JL_U）。
const queueFields = "QUEUE_NAME PRIORITY STATUS NJOBS PEND RUN SUSP MAX JL_H JL_P JL_U"

// bqueues -o 使用 PRIO；JSON 记录键为 PRIORITY（见 queueFields）。
const bqueuesOutputFields = "QUEUE_NAME PRIO STATUS MAX JL_U JL_P JL_H NJOBS PEND RUN SUSP"

func newLSFDriver() Scheduler {
	return &lsfDriver{
		sysEnvs: map[string]string{
			"LSB_SHORT_HOSTLIST":  "1",
			"LSB_DISPLAY_YEAR":    "Y",
			"LSB_HMS_TIME_FORMAT": "Y",
		},
		jobFields:   strings.Fields(jobFields),
		hostFields:  strings.Fields(hostFields),
		queueFields: strings.Fields(queueFields),
	}
}

const (
	// LSB_BJOBS_FORMAT。jobFields 是稳定的显示列；其后是本驱动额外请求的
	// 记录字段（bjobs -o 可自由扩展且零额外成本）。所有字段名均已在
	// LSF 10.1 实机验证有效，未知字段名会导致 bjobs 直接报错，故不要臆造。
	bjobsOutputFields = jobFields + " RUN_TIME EXEC_CWD OUTPUT_DIR INPUT_FILE OUTPUT_FILE ERROR_FILE" +
		" EXIT_CODE EXIT_REASON START_TIME FINISH_TIME PEND_TIME CPU_USED MEM SWAP PIDS SLOTS" +
		" PROJ_NAME JOB_PRIORITY APPLICATION EXEC_HOME NEXEC_HOST USER_GROUP JOB_GROUP CMD"
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
		Jobs: result.Records,
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
	lsfUser := cmp.Or(query.User, "all")
	args := []string{"-json", "-o", bjobsOutputFields, "-a", "-u", lsfUser}
	if query.Queue != "" {
		args = append(args, "-q", query.Queue)
	}
	if query.Node != "" {
		args = append(args, "-m", query.Node)
	}

	spec := payload.NewCommand("bjobs", args...)
	output, err := runJobQuery(ctx, exec, spec, d.SysEnvs())
	if err != nil {
		return JobListResult{}, err
	}
	return d.parseGetJobsOutput(output)
}

func (d *lsfDriver) ExecuteListHosts(ctx context.Context, exec execution.Executor) (HostListResult, error) {
	spec := payload.NewCommand("bhosts", "-json", "-o", bhostsOutputFields)

	output, err := runJobQuery(ctx, exec, spec, d.SysEnvs())
	if err != nil {
		return HostListResult{}, err
	}
	return parseLSFHostsOutput(output)
}

func (d *lsfDriver) ExecuteListQueues(ctx context.Context, exec execution.Executor) (QueueListResult, error) {
	spec := payload.NewCommand("bqueues", "-json", "-o", bqueuesOutputFields)

	output, err := runJobQuery(ctx, exec, spec, d.SysEnvs())
	if err != nil {
		return QueueListResult{}, err
	}
	return parseLSFQueuesOutput(output)
}

func (d *lsfDriver) ExecuteGetJob(ctx context.Context, exec execution.Executor, jobID string) (map[string]any, error) {
	spec := payload.NewCommand("bjobs", "-json", "-o", bjobsOutputFields, jobID)

	output, err := runJobQuery(ctx, exec, spec, d.SysEnvs())
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
		if isSchedulerJobNotFound(errors.New(errVal)) {
			return nil, ErrJobNotFound
		}
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

	output, err := runJobSubmit(ctx, exec, spec, req.Env, req.User, targetNode, nodeAffinity)
	if err != nil {
		return JobSubmitResult{}, err
	}
	return parseLSFSubmitOutput(output)
}

var lsfKeys = SchedulerKeyMap{
	ID:         "JOBID",
	User:       "USER",
	Status:     "STAT",
	SubmitTime: "SUBMIT_TIME",
}

// lsfActionStatuses 只列出该动作在 LSF 上确实能执行的 STAT。
// kill 含 PROV：作业已派发、主机/镜像仍在置备，bkill 可以取消。
// bstop 只作用于 PEND（→PSUSP）和 RUN（→USUSP）。
// bresume 不能恢复 SSUSP，系统挂起由 LSF 自己恢复。
// btop / bbot / bswitch 只对还在队列里的 PEND 有意义；PROV 已经派发。
var lsfActionStatuses = map[JobOp][]string{
	JobOpKill:     {"PEND", "RUN", "PROV", "PSUSP", "USUSP", "SSUSP", "WAIT"},
	JobOpSuspend:  {"PEND", "RUN"},
	JobOpResume:   {"PSUSP", "USUSP"},
	JobOpToTop:    {"PEND"},
	JobOpToBottom: {"PEND"},
	JobOpSwitch:   {"PEND"},
}

func (d *lsfDriver) Meta() SchedulerMeta {
	return SchedulerMeta{
		Fields: SchedulerDisplayFields{
			Job:   slices.Clone(d.jobFields),
			Host:  slices.Clone(d.hostFields),
			Queue: slices.Clone(d.queueFields),
		},
		Keys:    lsfKeys,
		Actions: cloneActionStatuses(lsfActionStatuses),
	}
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
	_, err := runJobControl(ctx, exec, spec, d.SysEnvs(), runUser)
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
	_, err := runJobModify(ctx, exec, spec, runUser)
	return err
}

var lsfStatusMap = map[string]NormalizedJobStatus{
	"RUN":   JobStatusRunning,
	"PROV":  JobStatusRunning, // dispatched; host/image still provisioning, slot already taken
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

// bhostsOutputFields 与 hostFields 一致；JL/U 在 -o 中必须写成 JL_U。
const bhostsOutputFields = hostFields

type bhostsJSONResponse struct {
	Hosts   int              `json:"HOSTS"`
	Records []map[string]any `json:"RECORDS"`
}

type bqueuesJSONResponse struct {
	Queues  int              `json:"QUEUES"`
	Records []map[string]any `json:"RECORDS"`
}

func parseLSFHostsOutput(output string) (HostListResult, error) {
	var result bhostsJSONResponse
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return HostListResult{}, fmt.Errorf("failed to parse bhosts json output: %w", err)
	}

	hosts := make([]map[string]any, 0, len(result.Records))
	onlineCount := 0
	for _, rec := range result.Records {
		name, _ := rec["HOST_NAME"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		if isLSFHostOnline(rec["STATUS"]) {
			onlineCount++
		}
		hosts = append(hosts, rec)
	}

	return HostListResult{
		Total:  len(hosts),
		Online: onlineCount,
		Hosts:  hosts,
	}, nil
}

func parseLSFQueuesOutput(output string) (QueueListResult, error) {
	var result bqueuesJSONResponse
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return QueueListResult{}, fmt.Errorf("failed to parse bqueues json output: %w", err)
	}
	queues := make([]map[string]any, 0, len(result.Records))
	for _, rec := range result.Records {
		name, _ := rec["QUEUE_NAME"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		queues = append(queues, rec)
	}
	return QueueListResult{Queues: queues}, nil
}

func isLSFHostOnline(status any) bool {
	s, _ := status.(string)
	statusLower := strings.ToLower(strings.TrimSpace(s))
	return statusLower == "ok" || statusLower == "closed" ||
		strings.HasPrefix(statusLower, "ok") || strings.HasPrefix(statusLower, "closed")
}
