package scheduler

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/execution"
)

func unsupportedJobControlOperation(op JobOp) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedJobControlOperation, op)
}

// NormalizedJobStatus is the normalized status category across all schedulers.
type NormalizedJobStatus string

const (
	JobStatusRunning   NormalizedJobStatus = "running"
	JobStatusPending   NormalizedJobStatus = "pending"
	JobStatusSuspended NormalizedJobStatus = "suspended"
	JobStatusCompleted NormalizedJobStatus = "completed"
	JobStatusFailed    NormalizedJobStatus = "failed"
	JobStatusUnknown   NormalizedJobStatus = "unknown"
)

// QueueStats captures job count stats for a queue/partition.
type QueueStats struct {
	Name      string `json:"name"`
	Allocated int    `json:"allocated"` // 队列中分配/活跃的作业数
	Running   int    `json:"running"`   // 运行中的作业数
	Pending   int    `json:"pending"`   // 等待中的作业数
	Suspended int    `json:"suspended"` // 暂停中的作业数
	Completed int    `json:"completed"` // 完成的作业数
	Failed    int    `json:"failed"`    // 失败的作业数
}

// HostStats captures statistics for an HPC compute host.
type HostStats struct {
	Name      string `json:"name"`
	Status    string `json:"status"`    // 原始状态（如 LSF "ok"/"closed"/"unavail"，Slurm "idle"/"allocated"/"down"）
	IsOnline  bool   `json:"isOnline"`  // 是否在线可用
	MaxSlots  int    `json:"maxSlots"`  // 最大可用槽数/核心数
	UsedSlots int    `json:"usedSlots"` // 已使用槽数/核心数
	RunJobs   int    `json:"runJobs"`   // 运行中的作业数
}

// HostListResult contains the list of scheduler host statistics.
type HostListResult struct {
	Total  int         `json:"total"`
	Online int         `json:"online"`
	Hosts  []HostStats `json:"hosts"`
}

// QueueListResult contains the list of queue statistics.
type QueueListResult struct {
	Queues []QueueStats `json:"queues"`
}

// Scheduler defines the scheduler-specific operations implemented by
// concrete adapters (LSF, Slurm) and consumed by the central Service.
type Scheduler interface {
	// --- Metadata ---

	// StatusKey returns the field name in a job map record that holds the raw status string.
	// e.g. "STAT" for LSF, "job_state" for Slurm.
	StatusKey() string
	// JobFields returns the recommended display columns for UI.
	JobFields() []string

	// --- Status normalization ---

	// NormalizeStatus maps a raw scheduler status string to a canonical NormalizedJobStatus.
	NormalizeStatus(raw string) NormalizedJobStatus

	// --- Job, Host & Queue lifecycle ---

	ExecuteListJobs(ctx context.Context, exec execution.Executor, query JobListQuery) (JobListResult, error)
	ExecuteListQueues(ctx context.Context, exec execution.Executor, jobs []map[string]any) (QueueListResult, error)
	ExecuteListHosts(ctx context.Context, exec execution.Executor) (HostListResult, error)
	ExecuteGetJob(ctx context.Context, exec execution.Executor, jobID string) (map[string]any, error)
	ExecuteSubmit(ctx context.Context, exec execution.Executor, req JobSubmitRequest, targetNode string, nodeAffinity execution.NodeAffinity) (JobSubmitResult, error)
	ExecuteControl(ctx context.Context, exec execution.Executor, jobID string, op JobOp, runUser string, args JobControlArgs) error
	ExecuteModify(ctx context.Context, exec execution.Executor, jobID string, args JobModifyArgs, runUser string) error
}

// JobSubmitRequest defines the data needed to submit a job.
type JobSubmitRequest struct {
	User    string
	Name    string
	Queue   string
	WorkDir string
	LogDir  string
	Cmd     string
	Env     map[string]string
}

// JobSubmitResult captures the metadata from a successful job submission.
type JobSubmitResult struct {
	JobID string
	Queue string
	Name  string
}

// JobListResult contains the structured results of a job query.
type JobListResult struct {
	Fields []string
	Jobs   []map[string]any
}

// JobListQuery 作业查询的过滤参数
type JobListQuery struct {
	User  string `query:"user"`
	Queue string `query:"queue"`
	Node  string `query:"node"`
}

// JobOp defines the type of control action to perform on a job.
type JobOp string

const (
	JobOpKill     JobOp = "kill"
	JobOpSuspend  JobOp = "suspend"
	JobOpResume   JobOp = "resume"
	JobOpToTop    JobOp = "toTop"
	JobOpToBottom JobOp = "toBottom"
	JobOpSwitch   JobOp = "switch"
)

// JobControlRequest defines the internal request for changing job state.
// JobIDs supports batch operations; User is injected server-side from the session.
type JobControlRequest struct {
	JobIDs      []string
	Operation   JobOp
	RequestUser string
	TargetQueue string
}

// JobControlArgs defines additional arguments for a job control operation.
type JobControlArgs struct {
	TargetQueue string
}

// JobModifyArgs defines the attributes that can be changed for a running job.
type JobModifyArgs struct {
	Queue *string `json:"queue,omitempty"` // -q: switch queue/partition
	Name  *string `json:"name,omitempty"`  // -n: rename job
}

// baseDriver provides common fields and methods for all scheduler drivers.
type baseDriver struct {
	envScript string
	sysEnvs   map[string]string // scheduler CLI 所需系统环境变量（与用户业务 Env 严格区分）
	jobFields []string          // 推荐前端显示的 job 信息字段
}

func (b *baseDriver) JobFields() []string {
	return b.jobFields
}

// SysEnvs returns the scheduler-required system environment variables.
// Returns nil when no extra environment is needed (e.g. Slurm).
func (b *baseDriver) SysEnvs() map[string]string {
	return b.sysEnvs
}

// aggregateQueuesFromJobs aggregates job statistics per queue/partition from a slice of raw job records.
func (b *baseDriver) aggregateQueuesFromJobs(jobs []map[string]any, queueKeyCandidates []string, statusKey string, normalizeStatus func(string) NormalizedJobStatus) QueueListResult {
	queueMap := make(map[string]*QueueStats)
	for _, job := range jobs {
		var qName string
		for _, k := range queueKeyCandidates {
			if v, ok := job[k].(string); ok && strings.TrimSpace(v) != "" {
				qName = strings.TrimSpace(v)
				break
			}
		}
		qName = cmp.Or(qName, "default")

		statusRaw, _ := job[statusKey].(string)
		status := normalizeStatus(statusRaw)

		// 支持逗号分隔的多个队列/分区（如 Slurm 的 "p1,p2"）
		parts := strings.SplitSeq(qName, ",")
		for p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			qs, ok := queueMap[p]
			if !ok {
				qs = &QueueStats{Name: p}
				queueMap[p] = qs
			}
			qs.Allocated++
			switch status {
			case JobStatusRunning:
				qs.Running++
			case JobStatusPending:
				qs.Pending++
			case JobStatusSuspended:
				qs.Suspended++
			case JobStatusCompleted:
				qs.Completed++
			case JobStatusFailed:
				qs.Failed++
			}
		}
	}

	queues := make([]QueueStats, 0, len(queueMap))
	for _, q := range queueMap {
		queues = append(queues, *q)
	}
	slices.SortFunc(queues, func(a, b QueueStats) int {
		return strings.Compare(a.Name, b.Name)
	})
	return QueueListResult{Queues: queues}
}

// newDriver instantiates the correct scheduler driver based on the configuration.
func newDriver(name config.JobScheduler, envScript string) Scheduler {
	switch name {
	case config.JobSchedulerLSF, config.JobSchedulerOpenLava:
		return newLSFDriver(envScript)
	case config.JobSchedulerSlurm:
		return newSlurmDriver(envScript)
	}
	return nil
}
