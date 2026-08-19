//go:build !windows

package action

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"design-cloud-engine/internal/common"
)

func TestRunCommandBoundsLargeStdout(t *testing.T) {
	t.Parallel()

	out, err := runCommand(
		context.Background(),
		nil,
		"sh",
		"",
		"-c",
		fmt.Sprintf("yes x | head -c %d", common.DefaultMaxCommandOutputBytes+1),
	)
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if !strings.Contains(out, "[output truncated at") {
		t.Fatalf("output did not include truncation notice")
	}
	if len(out) > common.DefaultMaxCommandOutputBytes+128 {
		t.Fatalf("output length = %d, want bounded near %d", len(out), common.DefaultMaxCommandOutputBytes)
	}
}
