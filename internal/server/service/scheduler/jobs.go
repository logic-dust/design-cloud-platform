package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/crypto"
)

// NormalizeJobStatus normalizes a raw job record from ListJobs into a canonical NormalizedJobStatus.
// The status field name is Meta.Keys.Status.
func (s *Service) NormalizeJobStatus(job map[string]any) NormalizedJobStatus {
	ops, meta := s.lastDriver()
	if ops == nil {
		return JobStatusUnknown
	}
	return ops.NormalizeStatus(rawStatusString(job[meta.Keys.Status]))
}

// SubmitAppJob submits an app-backed job to the scheduler.
func (s *Service) SubmitAppJob(ctx context.Context, appInfo *model.App, requestUser string) (map[string]string, error) {
	// Current assumption: the local engine node can execute scheduler CLI commands.
	// If that no longer holds, introduce a setting/selector for scheduler-capable nodes.
	targetNode := ""
	nodeAffinity := execution.NodeAffinityLocalPreferred

	now := time.Now()
	jobName := fmt.Sprintf("%02s%02d%02d%02d%02d_%s", strconv.Itoa(now.Year())[2:], now.Month(), now.Day(), now.Hour(), now.Minute(), crypto.GenRandomId(4))

	submitReq := JobSubmitRequest{
		User:  requestUser,
		Name:  jobName,
		Cmd:   appInfo.Cmd,
		Queue: appInfo.Queue,
	}

	ops, _, err := s.driver(ctx)
	if err != nil {
		return nil, err
	}
	res, err := ops.ExecuteSubmit(ctx, s.execSvc, submitReq, targetNode, nodeAffinity)
	if err != nil {
		return nil, execution.HandleError(s.logger, err, "job.submit")
	}

	jobInfo := map[string]string{
		"id":    res.JobID,
		"queue": res.Queue,
		"name":  jobName,
	}

	// Sync submit: operator learns job id from the HTTP response — no self-push.
	s.invalidateJobLists(requestUser)

	return jobInfo, nil
}

// GetJob returns a single job by ID.
func (s *Service) GetJob(ctx context.Context, jobID string) (map[string]any, error) {
	if err := ValidateJobID(jobID); err != nil {
		return nil, err
	}
	ops, _, err := s.driver(ctx)
	if err != nil {
		return nil, err
	}
	return ops.ExecuteGetJob(ctx, s.execSvc, jobID)
}

// 作业列表缓存键：<种类前缀><过滤条件>\x00<调度器类型>。
// 调度器类型位于末尾，使驱动切换前在途的旧驱动查询写回的结果落在旧类型的键上，
// 新类型永远读不到；种类前缀不含类型，按属主失效时一次覆盖所有类型。
const jobListCacheKeyClusterPrefix = "jobs:cluster:\x00"

func jobListCacheKeyUserPrefix(user string) string {
	return "jobs:user:\x00" + user + "\x00"
}

func jobListCacheKey(q JobListQuery, scheduler schema.JobScheduler) string {
	q.User = strings.TrimSpace(q.User)
	if q.User == "" {
		return jobListCacheKeyClusterPrefix + q.Queue + "\x00" + q.Node + "\x00" + string(scheduler)
	}
	return jobListCacheKeyUserPrefix(q.User) + q.Queue + "\x00" + q.Node + "\x00" + string(scheduler)
}

// ListJobs lists jobs. Empty query.User means every user (cluster-wide).
func (s *Service) ListJobs(ctx context.Context, query JobListQuery) (JobListResult, error) {
	query.User = strings.TrimSpace(query.User)
	ops, meta, err := s.driver(ctx)
	if err != nil {
		return JobListResult{}, err
	}
	result, err := s.jobCache.GetWithTTL(ctx, jobListCacheKey(query, meta.Scheduler), s.jobCacheTTL(ctx), func(ctx context.Context, _ string) (JobListResult, error) {
		return ops.ExecuteListJobs(ctx, s.execSvc, query)
	})
	if err != nil {
		return JobListResult{}, err
	}
	result.Scheduler = meta.Scheduler
	return result, nil
}

// ListQueues returns native queue/partition records from the scheduler.
func (s *Service) ListQueues(ctx context.Context) (QueueListResult, error) {
	ops, _, err := s.driver(ctx)
	if err != nil {
		return QueueListResult{}, err
	}
	if ops == nil {
		return QueueListResult{}, nil
	}
	return ops.ExecuteListQueues(ctx, s.execSvc)
}

// ListHosts returns compute host statistics from the HPC scheduler.
func (s *Service) ListHosts(ctx context.Context) (HostListResult, error) {
	ops, _, err := s.driver(ctx)
	if err != nil {
		return HostListResult{}, err
	}
	if ops == nil {
		return HostListResult{}, nil
	}
	return ops.ExecuteListHosts(ctx, s.execSvc)
}

// jobCacheTTL 返回作业列表缓存 TTL：跟随 scheduler 域 JobCacheTTL 设置。
//
// 读失败/未配置时返回 0，由 TTLCache 回退到构造时设定的代码默认值——
// "TTL<=0 走缓存级默认"是 cache 自身的契约，此处不重复一份默认值。
func (s *Service) jobCacheTTL(ctx context.Context) time.Duration {
	if s.settingsSvc == nil {
		return 0
	}
	settings, err := s.settingsSvc.GetSchedulerSettings(ctx)
	if err != nil {
		return 0
	}
	return time.Duration(settings.JobCacheTTL) * time.Second
}

// ControlJob changes job state for one or more jobs.
func (s *Service) ControlJob(ctx context.Context, control JobControlRequest) (map[string]string, error) {
	if len(control.JobIDs) > MaxBatchControlJobs {
		return nil, contract.NewInvalidParamError(fmt.Sprintf("jobIds cannot contain more than %d jobs", MaxBatchControlJobs))
	}
	if len(control.JobIDs) == 0 {
		return nil, contract.NewInvalidParamError("jobIds is required")
	}
	if _, ok := ParseJobOp(string(control.Operation)); !ok {
		return nil, unsupportedJobControlOperation(control.Operation)
	}
	for _, jobID := range control.JobIDs {
		if err := ValidateJobID(jobID); err != nil {
			return nil, err
		}
	}
	if control.Operation == JobOpSwitch {
		if err := validateArg("targetQueue", control.TargetQueue); err != nil {
			return nil, err
		}
	}

	ops, _, err := s.driver(ctx)
	if err != nil {
		return nil, err
	}
	failures := make(map[string]string)
	for _, jobID := range control.JobIDs {
		args := JobControlArgs{TargetQueue: control.TargetQueue}
		if err := ops.ExecuteControl(ctx, s.execSvc, jobID, control.Operation, control.RequestUser, args); err != nil {
			if errors.Is(err, ErrUnsupportedJobControlOperation) {
				return nil, err
			}
			msg := err.Error()
			if isSchedulerJobNotFound(err) {
				msg = fmt.Sprintf("job %s not found", jobID)
			}
			failures[jobID] = msg
		}
	}

	// 作业控制是同步链路（scancel/scontrol 立即返回）：操作者从 response
	// 拿到结果（failures）。命令以 RequestUser 身份执行，调度器只允许其操作
	// 本人作业，故 RequestUser 即作业属主：仅失效列表缓存，不对操作者推送。
	s.invalidateJobLists(control.RequestUser)
	return failures, nil
}

// ModifyJob modifies attributes of a job (queue, name). runUser is the job owner
// the command runs as.
func (s *Service) ModifyJob(ctx context.Context, jobID string, args JobModifyArgs, runUser string) error {
	if err := ValidateJobID(jobID); err != nil {
		return err
	}
	if args.Queue == nil && args.Name == nil {
		return contract.NewInvalidParamError("at least one attribute must be specified for modify")
	}
	if args.Queue != nil {
		if err := validateArg("queue", *args.Queue); err != nil {
			return err
		}
	}
	if args.Name != nil {
		if err := validateArg("name", *args.Name); err != nil {
			return err
		}
	}
	ops, _, err := s.driver(ctx)
	if err != nil {
		return err
	}
	if err := ops.ExecuteModify(ctx, s.execSvc, jobID, args, runUser); err != nil {
		return err
	}
	// Sync modify: operator gets success/error from the response — no self-push.
	s.invalidateJobLists(runUser)
	return nil
}

// invalidateJobLists 失效全集群作业列表缓存与指定属主的个人列表缓存，
// 其他用户的个人列表缓存保持命中。owner 为空时（无法确定属主）清空全部。
func (s *Service) invalidateJobLists(owner string) {
	if owner == "" {
		s.jobCache.Clear()
		return
	}
	userPrefix := jobListCacheKeyUserPrefix(owner)
	s.jobCache.DeleteFunc(func(key string) bool {
		return strings.HasPrefix(key, jobListCacheKeyClusterPrefix) || strings.HasPrefix(key, userPrefix)
	})
}

func isSchedulerJobNotFound(err error) bool {
	if errors.Is(err, ErrJobNotFound) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "job not found") ||
		strings.Contains(msg, "is not found") ||
		strings.Contains(msg, "unknown job id") ||
		strings.Contains(msg, "invalid job id specified")
}
