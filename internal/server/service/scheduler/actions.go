package scheduler

import (
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

func runListJobsQuery(ctx context.Context, exec execution.Executor, spec payload.CommandSpec, env map[string]string, envScript string, query JobListQuery) (string, error) {
	jobPayload := payload.JobQuery{
		Command:   &spec,
		Env:       env,
		EnvScript: envScript,
		User:      query.User,
		Queue:     query.Queue,
	}
	return runSchedulerAction(ctx, exec, execution.ActionJobQuery, jobPayload, "", "", execution.NodeAffinityLocalPreferred)
}

func runListHostsQuery(ctx context.Context, exec execution.Executor, spec payload.CommandSpec, env map[string]string, envScript string) (string, error) {
	jobPayload := payload.JobQuery{
		Command:   &spec,
		Env:       env,
		EnvScript: envScript,
	}
	return runSchedulerAction(ctx, exec, execution.ActionJobQuery, jobPayload, "", "", execution.NodeAffinityLocalPreferred)
}

func runGetJobQuery(ctx context.Context, exec execution.Executor, spec payload.CommandSpec, env map[string]string, envScript string, jobID string) (string, error) {
	jobPayload := payload.JobQuery{
		Command:   &spec,
		Env:       env,
		EnvScript: envScript,
		JobID:     jobID,
	}
	return runSchedulerAction(ctx, exec, execution.ActionJobQuery, jobPayload, "", "", execution.NodeAffinityLocalPreferred)
}

func runJobSubmit(ctx context.Context, exec execution.Executor, spec payload.CommandSpec, req JobSubmitRequest, envScript string, targetNode string, nodeAffinity execution.NodeAffinity) (string, error) {
	jobPayload := payload.JobSubmit{
		Command:   &spec,
		Username:  req.User,
		JobName:   req.Name,
		Queue:     req.Queue,
		WorkDir:   req.WorkDir,
		LogDir:    req.LogDir,
		Env:       req.Env,
		EnvScript: envScript,
	}
	return runSchedulerAction(ctx, exec, execution.ActionJobSubmit, jobPayload, req.User, targetNode, nodeAffinity)
}

func runJobControl(ctx context.Context, exec execution.Executor, spec payload.CommandSpec, env map[string]string, envScript string, jobID string, op JobOp, runUser, targetNode, targetQueue string) (string, error) {
	jobPayload := payload.JobControl{
		Command:     &spec,
		Env:         env,
		EnvScript:   envScript,
		Operation:   string(op),
		JobID:       jobID,
		TargetQueue: targetQueue,
	}
	return runSchedulerAction(ctx, exec, execution.ActionJobControl, jobPayload, runUser, targetNode, execution.NodeAffinityLocalPreferred)
}

func runJobModify(ctx context.Context, exec execution.Executor, spec payload.CommandSpec, envScript string, jobID string, args JobModifyArgs, runUser string) (string, error) {
	jobPayload := payload.JobModify{
		Command:   &spec,
		EnvScript: envScript,
		JobID:     jobID,
		Queue:     args.Queue,
		Name:      args.Name,
	}
	return runSchedulerAction(ctx, exec, execution.ActionJobModify, jobPayload, runUser, "", execution.NodeAffinityLocalPreferred)
}

func runSchedulerAction(ctx context.Context, exec execution.Executor, actionName execution.ActionName, payloadData any, runUser, targetNode string, nodeAffinity execution.NodeAffinity) (string, error) {
	executionID := fmt.Sprintf("sched-exec-%d", time.Now().UnixNano())
	req := execution.NewExecuteRequest(actionName, executionID,
		execution.WithTargetUser(runUser),
		execution.WithTargetNode(targetNode),
		execution.WithNodeAffinity(nodeAffinity),
		execution.WithPayload(payloadData),
	)
	return execution.Run(ctx, exec, req)
}
