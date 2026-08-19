//go:build !windows

package action

import (
	"context"
	"fmt"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

// HandleJobSubmit submits a job using the pre-assembled scheduler command.
// Environment variables from the payload are exported into the process so
// schedulers like LSF inherit them into the submitted job environment.
// The raw stdout (e.g. "Job <123> is submitted to queue <normal>") is returned
// to the server for driver-side parsing.
func HandleJobSubmit(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.JobSubmit](req)
	if err != nil {
		return "", fmt.Errorf("failed to parse job submit payload: %w", err)
	}
	if p.Command == nil {
		return "", fmt.Errorf("job submit command is required")
	}
	return runJobCommand(ctx, buildJobEnvs(p.Env), p.EnvScript, p.Command)
}

// HandleJobModify modifies job attributes (queue, name, etc.) using the pre-assembled
// scheduler command. The command is built by the server-side scheduler driver and
// executed directly without a shell wrapper.
func HandleJobModify(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.JobModify](req)
	if err != nil {
		return "", fmt.Errorf("failed to parse job modify payload: %w", err)
	}
	if p.Command == nil {
		return "", fmt.Errorf("job modify command is required")
	}
	return runJobCommand(ctx, nil, p.EnvScript, p.Command)
}

// HandleJobControl sends a control command (kill, suspend, resume, etc.) to a job.
// The command is built by the server-side scheduler driver and executed directly.
func HandleJobControl(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.JobControl](req)
	if err != nil {
		return "", fmt.Errorf("failed to parse job control payload: %w", err)
	}
	if p.Command == nil {
		return "", fmt.Errorf("job control command is required")
	}
	return runJobCommand(ctx, buildJobEnvs(p.Env), p.EnvScript, p.Command)
}

// buildJobEnvs converts the env map to a "KEY=VALUE" slice suitable for runCommand.
// Keys that are blocked for cross-privilege-boundary use (e.g. LD_PRELOAD) or
// that contain unsafe characters are silently dropped.
func buildJobEnvs(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	envs := make([]string, 0, len(m))
	for k, v := range m {
		if !execution.IsValidEnvKey(k) || execution.IsBlockedEnvKey(k) {
			continue
		}
		envs = append(envs, fmt.Sprintf("%s=%s", k, v))
	}
	return envs
}

func runJobCommand(ctx context.Context, envs []string, envScript string, spec *payload.CommandSpec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", fmt.Errorf("invalid job command: %w", err)
	}
	if envScript != "" {
		args := append([]string{"-c", ". \"$0\" && exec \"$@\"", envScript, spec.Program}, spec.Args...)
		return runCommandWithDir(ctx, envs, spec.WorkDir, "bash", "", args...)
	}
	return runCommandWithDir(ctx, envs, spec.WorkDir, spec.Program, "", spec.Args...)
}
