//go:build !windows

package action

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/platform/process"
)

func TestRunCommandBoundsLargeStdout(t *testing.T) {
	t.Parallel()

	out, err := runCommand(
		context.Background(),
		nil,
		"sh",
		"",
		"-c",
		fmt.Sprintf("yes x | head -c %d", process.DefaultMaxCommandOutputBytes+1),
	)
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if !strings.Contains(out, "[output truncated at") {
		t.Fatalf("output did not include truncation notice")
	}
	if len(out) > process.DefaultMaxCommandOutputBytes+128 {
		t.Fatalf("output length = %d, want bounded near %d", len(out), process.DefaultMaxCommandOutputBytes)
	}
}

func TestRunCommandCancellationKillsProcessGroup(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runCommand(ctx, nil, "sh", "", "-c", "sleep 2 & wait")
	if err == nil {
		t.Fatal("runCommand succeeded after its context expired")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runCommand took %s after timeout; a child process may have survived", elapsed)
	}
}

func TestValidatePathResolvesExistingParentForNewTargets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	link := filepath.Join(dir, "protected")
	if err := os.Symlink("/etc", link); err != nil {
		t.Skipf("create symlink: %v", err)
	}
	newTarget := filepath.Join(link, "new-file")
	if err := ValidatePath("path", newTarget); err == nil {
		t.Fatalf("ValidatePath accepted new target under protected symlink: %q", newTarget)
	}
}

func TestValidatePathRejectsDanglingParentSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "missing-target"), link); err != nil {
		t.Skipf("create symlink: %v", err)
	}
	if err := ValidatePath("path", filepath.Join(link, "new-file")); err == nil {
		t.Fatal("ValidatePath accepted a target under an unresolved symlink")
	}
}
