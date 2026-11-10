package scheduler

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"design-cloud-engine/internal/contract/schema"
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

// HostListResult contains native scheduler host records (same key style as jobs).
// Total/Online are convenience counters derived from status; hosts themselves are not collapsed.
type HostListResult struct {
	Total  int              `json:"total"`
	Online int              `json:"online"`
	Hosts  []map[string]any `json:"hosts"`
}

// QueueListResult contains native scheduler queue/partition records.
type QueueListResult struct {
	Queues []map[string]any `json:"queues"`
}

// SchedulerKeyMap 声明客户端从原生作业记录中提取核心字段时使用的键名。
// 客户端应用 keys.user / keys.status 定位用户与状态列，不要假设固定列序或固定原生名。
type SchedulerKeyMap struct {
	ID         string `json:"id"`
	User       string `json:"user"`
	Status     string `json:"status"`
	SubmitTime string `json:"submitTime"`
}

// SchedulerDisplayFields 按资源列出建议默认展示的原生列（有序）。
type SchedulerDisplayFields struct {
	Job   []string `json:"job"`
	Host  []string `json:"host"`
	Queue []string `json:"queue"`
}

// SchedulerMeta 描述当前调度器的推荐展示列、核心字段映射与各动作允许的原生状态集合。
type SchedulerMeta struct {
	Scheduler schema.JobScheduler    `json:"scheduler"`
	Fields    SchedulerDisplayFields `json:"fields"`
	Keys      SchedulerKeyMap        `json:"keys"` // 作业核心语义 → 原生键
	Actions   map[JobOp][]string     `json:"actions"`
}

// Scheduler defines the scheduler-specific operations implemented by
// concrete adapters (LSF, Slurm) and consumed by the central Service.
type Scheduler interface {
	// --- Metadata ---

	// Meta returns the driver-declared metadata (Fields, Keys, Actions).
	// Keys.Status is the native status field (LSF "STAT", Slurm "job_state").
	Meta() SchedulerMeta

	// --- Status normalization ---

	// NormalizeStatus maps a raw scheduler status string to a canonical NormalizedJobStatus.
	NormalizeStatus(raw string) NormalizedJobStatus

	// --- Job, Host & Queue lifecycle ---

	// ExecuteListJobs lists jobs. An empty query.User means every user.
	ExecuteListJobs(ctx context.Context, exec execution.Executor, query JobListQuery) (JobListResult, error)
	ExecuteListQueues(ctx context.Context, exec execution.Executor) (QueueListResult, error)
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
	Scheduler schema.JobScheduler `json:"scheduler"`
	Jobs      []map[string]any    `json:"jobs"`
}

// JobListQuery filters a job list. Empty User means all users (cluster-wide).
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

// ParseJobOp accepts the single whitelist shared by path :op, batch operation, and meta.actions keys.
func ParseJobOp(s string) (JobOp, bool) {
	switch JobOp(s) {
	case JobOpKill, JobOpSuspend, JobOpResume, JobOpToTop, JobOpToBottom, JobOpSwitch:
		return JobOp(s), true
	default:
		return "", false
	}
}

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
	sysEnvs     map[string]string // scheduler CLI 所需系统环境变量（与用户业务 Env 严格区分）
	jobFields   []string          // 推荐前端显示的 job 信息字段
	hostFields  []string          // 推荐前端显示的 host 原生字段
	queueFields []string          // 推荐前端显示的 queue/partition 原生字段
}

// SysEnvs returns the scheduler-required system environment variables.
// Returns nil when no extra environment is needed (e.g. Slurm).
func (b *baseDriver) SysEnvs() map[string]string {
	return b.sysEnvs
}

// cloneActionStatuses copies an action allow-list so callers cannot mutate the
// package-level tables or the Service cache through a returned SchedulerMeta.
func cloneActionStatuses(src map[JobOp][]string) map[JobOp][]string {
	if src == nil {
		return nil
	}
	out := make(map[JobOp][]string, len(src))
	for action, statuses := range src {
		out[action] = slices.Clone(statuses)
	}
	return out
}

// detachedMeta copies the slices a caller could mutate. Keys and Scheduler are values.
func detachedMeta(meta SchedulerMeta) SchedulerMeta {
	meta.Fields.Job = slices.Clone(meta.Fields.Job)
	meta.Fields.Host = slices.Clone(meta.Fields.Host)
	meta.Fields.Queue = slices.Clone(meta.Fields.Queue)
	meta.Actions = cloneActionStatuses(meta.Actions)
	return meta
}

// rawStatusString extracts the raw status value from a job record field.
// LSF "STAT" is a plain string. Slurm "job_state" is a list whose first
// non-empty element is the base state and any later elements are flags
// (e.g. ["CONFIGURING", "POWER_UP_NODE"]). Action allow-lists compare that
// first element, not the whole list.
func rawStatusString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []any:
		for _, item := range val {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

// projectJob returns a shallow copy of job containing only the given keys.
// Values are shared, not deep-copied. Missing keys are skipped silently so the
// projection tolerates scheduler-version field drift (e.g. an older Slurm
// without state_reason).
func projectJob(job map[string]any, keep []string) map[string]any {
	out := make(map[string]any, len(keep))
	for _, k := range keep {
		if v, ok := job[k]; ok {
			out[k] = v
		}
	}
	return out
}

// newDriver instantiates the correct scheduler driver based on the configuration.
func newDriver(name schema.JobScheduler) Scheduler {
	switch name {
	case schema.JobSchedulerLSF, schema.JobSchedulerOpenLava:
		return newLSFDriver()
	case schema.JobSchedulerSlurm:
		return newSlurmDriver()
	}
	return nil
}
