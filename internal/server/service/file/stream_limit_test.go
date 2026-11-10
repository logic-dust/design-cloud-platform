//go:build linux

package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"design-cloud-engine/internal/execution"
)

func newFakeDcExecExecutor(t *testing.T, script string) *StreamExecutor {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "dcexec")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat >/dev/null\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("tok"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &StreamExecutor{getExecBinPath: func(context.Context) string { return bin }, tokenFile: tokenFile}
}

func TestStreamExecuteRejectsOversizedResult(t *testing.T) {
	e := newFakeDcExecExecutor(t, "head -c 33554433 /dev/zero")
	_, err := e.Execute(context.Background(), execution.NewExecuteRequest(execution.ActionFileList, "t"))
	if !errors.Is(err, ErrDcExecResultTooLarge) {
		t.Fatalf("err = %v, want ErrDcExecResultTooLarge", err)
	}
}

func TestStreamExecuteBoundsStderr(t *testing.T) {
	e := newFakeDcExecExecutor(t, "head -c 1048576 /dev/zero | tr '\\0' 'x' >&2; exit 1")
	_, err := e.Execute(context.Background(), execution.NewExecuteRequest(execution.ActionFileList, "t"))
	if err == nil {
		t.Fatal("expected error")
	}
	if len(err.Error()) > dcexecStderrLimit+256 {
		t.Fatalf("error message length %d exceeds stderr limit", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "output truncated") {
		t.Fatalf("expected truncation notice in %q", err.Error()[:120])
	}
}
