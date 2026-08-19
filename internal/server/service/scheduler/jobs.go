package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"design-cloud-engine/internal/common/crypto"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/push"
)

// NormalizeJobStatus normalizes a raw job record from ListJobs into a canonical NormalizedJobStatus.
// The status field name is scheduler-specific and declared by the driver via StatusKey().
func (s *Service) NormalizeJobStatus(job map[string]any) NormalizedJobStatus {
	if s.ops == nil {
		return JobStatusUnknown
	}
	val, _ := job[s.ops.StatusKey()].(string)
	return s.ops.NormalizeStatus(val)
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

	res, err := s.ops.ExecuteSubmit(ctx, s.execSvc, submitReq, targetNode, nodeAffinity)
	if err != nil {
		return nil, execution.HandleError(s.logger, err, "job.submit")
	}

	jobInfo := map[string]string{
		"id":    res.JobID,
		"queue": res.Queue,
		"name":  jobName,
	}

	s.jobCache.Clear()
	s.notifyUserJobRefresh(ctx, requestUser, res.JobID, "refresh")

	return jobInfo, nil
}

// GetJob returns a single job by ID.
func (s *Service) GetJob(ctx context.Context, jobID string) (map[string]any, error) {
	return s.ops.ExecuteGetJob(ctx, s.execSvc, jobID)
}

// ListJobs returns jobs with optional scheduler-side filters.
// Results are cached per-query to avoid hammering the external scheduler CLI.
// TTL 跟随 settings.JobCacheTTL（scheduler 域，默认 60s），每次调用动态读取
// 支持热更新；0/读取失败回退代码默认 60s。
func (s *Service) ListJobs(ctx context.Context, query JobListQuery) (JobListResult, error) {
	cacheKey := query.User + "|" + query.Queue + "|" + query.Node
	return s.jobCache.GetWithTTL(ctx, cacheKey, s.jobCacheTTL(ctx), func(ctx context.Context, _ string) (JobListResult, error) {
		return s.ops.ExecuteListJobs(ctx, s.execSvc, query)
	})
}

// ListQueues returns aggregated queue statistics across the scheduler.
func (s *Service) ListQueues(ctx context.Context) (QueueListResult, error) {
	if s.ops == nil {
		return QueueListResult{}, nil
	}
	jobResult, err := s.ListJobs(ctx, JobListQuery{})
	var jobs []map[string]any
	if err == nil {
		jobs = jobResult.Jobs
	}
	return s.ops.ExecuteListQueues(ctx, s.execSvc, jobs)
}

// ListHosts returns compute host statistics from the HPC scheduler.
func (s *Service) ListHosts(ctx context.Context) (HostListResult, error) {
	if s.ops == nil {
		return HostListResult{}, nil
	}
	return s.ops.ExecuteListHosts(ctx, s.execSvc)
}

// jobCacheTTL 返回作业列表缓存 TTL：跟随 scheduler 域 JobCacheTTL 设置。
//
// 读失败/未配置时返回 0，由 TTLCache 回退到构造时设定的代码默认值——
// "TTL<=0 走缓存级默认"是 cache 自身的契约，此处不重复一份默认值。
func (s *Service) jobCacheTTL(ctx context.Context) time.Duration {
	settings, err := s.settingsSvc.GetSchedulerSettings(ctx)
	if err != nil {
		return 0
	}
	return time.Duration(settings.JobCacheTTL) * time.Second
}

// ControlJob changes job state for one or more jobs.
func (s *Service) ControlJob(ctx context.Context, control JobControlRequest) (map[string]string, error) {
	if control.Operation == JobOpSwitch && control.TargetQueue == "" {
		return nil, fmt.Errorf("target queue is required for switch operation")
	}

	failures := make(map[string]string)
	for _, jobID := range control.JobIDs {
		args := JobControlArgs{TargetQueue: control.TargetQueue}
		if err := s.ops.ExecuteControl(ctx, s.execSvc, jobID, control.Operation, control.RequestUser, args); err != nil {
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
	// 拿到结果（failures），同时失效本地查询缓存并向操作者推送作业刷新事件
	s.jobCache.Clear()
	s.notifyUserJobRefresh(ctx, control.RequestUser, "", "refresh")
	return failures, nil
}

// ModifyJob modifies attributes of a running job (queue, name, etc.).
func (s *Service) ModifyJob(ctx context.Context, jobID string, args JobModifyArgs, runUser string) error {
	if args.Queue == nil && args.Name == nil {
		return fmt.Errorf("at least one attribute must be specified for modify")
	}
	if err := s.ops.ExecuteModify(ctx, s.execSvc, jobID, args, runUser); err != nil {
		return err
	}
	s.jobCache.Clear()
	s.notifyUserJobRefresh(ctx, runUser, jobID, "refresh")
	return nil
}

func (s *Service) notifyUserJobRefresh(ctx context.Context, owner, jobID, action string) {
	if s.publisher == nil {
		return
	}
	msg := push.Message{
		Name: push.EventJob,
		Payload: push.EventPayload{
			Action:   action,
			TargetID: jobID,
		},
	}
	if owner == "" {
		s.publisher.Broadcast(ctx, msg)
		return
	}
	s.publisher.PublishToUser(ctx, []string{owner}, msg)
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
