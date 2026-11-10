//go:build !windows

package direct

import (
	"context"
	"fmt"

	"design-cloud-engine/internal/execution"
)

func handleSessionControl(_ context.Context, _ *execution.ExecuteRequest) (string, error) {
	return "", fmt.Errorf("SESSION_CONTROL direct handler is Windows-only; Linux uses dc-exec")
}
