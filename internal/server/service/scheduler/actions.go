package scheduler

import (
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

func runJobQuery(ctx context.Context, exec execution.Executor, spec payload.CommandSpec, env map[string]string) (string, error) {
	return runSchedulerAction(ctx, exec, execution.ActionJobQuery, payload.JobQuery{
		Command: &spec,
		Env:     env,
	}, "", "", execution.NodeAffinityLocalPreferred)
}

func runJobSubmit(ctx context.Context, exec execution.Executor, spec payload.CommandSpec, env map[string]string, runUser, targetNode string, nodeAffinity execution.NodeAffinity) (string, error) {
	return runSchedulerAction(ctx, exec, execution.ActionJobSubmit, payload.JobSubmit{
		Command: &spec,
		Env:     env,
	}, runUser, targetNode, nodeAffinity)
}

func runJobControl(ctx context.Context, exec execution.Executor, spec payload.CommandSpec, env map[string]string, runUser string) (string, error) {
	return runSchedulerAction(ctx, exec, execution.ActionJobControl, payload.JobControl{
		Command: &spec,
		Env:     env,
	}, runUser, "", execution.NodeAffinityLocalPreferred)
}

func runJobModify(ctx context.Context, exec execution.Executor, spec payload.CommandSpec, runUser string) (string, error) {
	return runSchedulerAction(ctx, exec, execution.ActionJobModify, payload.JobModify{
		Command: &spec,
	}, runUser, "", execution.NodeAffinityLocalPreferred)
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
