//go:build !windows

package file

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

func TestHandleFileReadSmallFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	out, err := HandleFileRead(context.Background(), execution.NewExecuteRequest(execution.ActionFileRead, "read-small",
		execution.WithPayload(&payload.FileRead{Path: path}),
	))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(out)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if string(decoded) != "secret" {
		t.Fatalf("decoded output = %q, want secret", string(decoded))
	}
}

func TestHandleFileReadRejectsDirectory(t *testing.T) {
	t.Parallel()

	_, err := HandleFileRead(context.Background(), execution.NewExecuteRequest(execution.ActionFileRead, "read-dir",
		execution.WithPayload(&payload.FileRead{Path: t.TempDir()}),
	))
	if err == nil || !strings.Contains(err.Error(), "path is a directory") {
		t.Fatalf("error = %v, want directory error", err)
	}
}

func TestHandleFileReadRejectsTooLargeFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "large")
	data := make([]byte, maxFileReadBytes+1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := HandleFileRead(context.Background(), execution.NewExecuteRequest(execution.ActionFileRead, "read-large",
		execution.WithPayload(&payload.FileRead{Path: path}),
	))
	if err == nil || !strings.Contains(err.Error(), "file too large") {
		t.Fatalf("error = %v, want size error", err)
	}
}

func TestHandleFileDeleteRejectsInvalidEntryWithoutDeleting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		files []string
	}{
		{name: "parent", files: []string{"..", "keep"}},
		{name: "current", files: []string{".", "keep"}},
		{name: "separator", files: []string{"a/b", "keep"}},
		{name: "null", files: []string{"bad\x00name", "keep"}},
		{name: "newline", files: []string{"bad\nname", "keep"}},
		{name: "invalid after valid", files: []string{"keep", ".."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			keep := filepath.Join(dir, "keep")
			if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
				t.Fatalf("write keep file: %v", err)
			}

			_, err := HandleFileDelete(context.Background(), execution.NewExecuteRequest(execution.ActionFileDelete, "delete-invalid",
				execution.WithPayload(&payload.FileDelete{Dir: dir, Files: tc.files}),
			))
			if err == nil || !strings.Contains(err.Error(), "invalid file name") {
				t.Fatalf("error = %v, want invalid file name error", err)
			}
			if _, statErr := os.Stat(keep); statErr != nil {
				t.Fatalf("keep file was removed after invalid input: %v", statErr)
			}
		})
	}
}

func TestHandleFileDeleteDeletesValidEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("delete me"), 0o600); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	out, err := HandleFileDelete(context.Background(), execution.NewExecuteRequest(execution.ActionFileDelete, "delete-valid",
		execution.WithPayload(&payload.FileDelete{Dir: dir, Files: []string{"target"}}),
	))
	if err != nil {
		t.Fatalf("delete valid file: %v", err)
	}
	if out != "Deleted 1 files" {
		t.Fatalf("output = %q, want Deleted 1 files", out)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target still exists or unexpected stat error: %v", err)
	}
}
