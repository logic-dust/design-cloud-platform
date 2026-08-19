package direct

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

// handleUserList returns the raw content of /etc/passwd
func handleUserList(_ context.Context, _ *execution.ExecuteRequest) (string, error) {
	b, err := os.ReadFile("/etc/passwd")
	return string(b), err
}

// handleGroupList returns the raw content of /etc/group
func handleGroupList(_ context.Context, _ *execution.ExecuteRequest) (string, error) {
	b, err := os.ReadFile("/etc/group")
	return string(b), err
}

// handleUserGet looks up a single user using getent passwd
func handleUserGet(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.UserGet](req)
	if err != nil {
		return "", err
	}
	b, err := exec.CommandContext(ctx, "getent", "passwd", p.Username).Output()
	if err != nil {
		return "", fmt.Errorf("user not found or getent failed: %w", err)
	}
	return string(b), nil
}

// handleGroupGet looks up a single group using getent group
func handleGroupGet(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.GroupGet](req)
	if err != nil {
		return "", err
	}
	b, err := exec.CommandContext(ctx, "getent", "group", p.GroupName).Output()
	if err != nil {
		return "", fmt.Errorf("group not found or getent failed: %w", err)
	}
	return string(b), nil
}
