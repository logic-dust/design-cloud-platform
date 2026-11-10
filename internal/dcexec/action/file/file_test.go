//go:build !windows

package file

import (
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"encoding/base64"
	"fmt"
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

func TestHandleFileListRequiresPositiveLimit(t *testing.T) {
	t.Parallel()

	_, err := HandleFileList(context.Background(), execution.NewExecuteRequest(execution.ActionFileList, "list",
		execution.WithPayload(&payload.FileList{Dir: t.TempDir(), Limit: 0}),
	))
	if err == nil {
		t.Fatal("expected error for limit=0")
	}
}

func TestHandleFileListSortsMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"c", "a"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Symlink("a", filepath.Join(dir, "b")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	out, err := HandleFileList(context.Background(), execution.NewExecuteRequest(execution.ActionFileList, "list",
		execution.WithPayload(&payload.FileList{Dir: dir, Limit: 100}),
	))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listed fileListResult
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("unmarshal %s: %v", out, err)
	}
	if listed.Truncated {
		t.Fatal("truncated = true, want false")
	}
	if len(listed.Files) != 3 {
		t.Fatalf("len = %d, want 3", len(listed.Files))
	}
	got := []string{listed.Files[0].Name, listed.Files[1].Name, listed.Files[2].Name}
	want := []string{"a", "b", "c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("names = %v, want %v", got, want)
	}
	if !listed.Files[1].IsSymlink || listed.Files[1].LinkTarget != "a" {
		t.Fatalf("symlink = %+v, want link to a", listed.Files[1])
	}
}

func TestListDirReturnsSlackThenTruncates(t *testing.T) {
	t.Parallel()

	const window = 2
	capN := listReadCap(window)

	within := t.TempDir()
	writeEmptyFiles(t, within, 3)
	files, truncated, err := listDir(within, window)
	if err != nil {
		t.Fatalf("list within slack: %v", err)
	}
	if truncated || len(files) != 3 {
		t.Fatalf("within slack: len=%d truncated=%v, want 3 and false", len(files), truncated)
	}
	if files[0].Name > files[1].Name {
		t.Fatalf("names not sorted: %s, %s", files[0].Name, files[1].Name)
	}

	exact := t.TempDir()
	writeEmptyFiles(t, exact, capN)
	files, truncated, err = listDir(exact, window)
	if err != nil {
		t.Fatalf("list exact cap: %v", err)
	}
	if truncated || len(files) != capN {
		t.Fatalf("exact cap: len=%d truncated=%v, want %d and false", len(files), truncated, capN)
	}

	over := t.TempDir()
	writeEmptyFiles(t, over, capN+1)
	files, truncated, err = listDir(over, window)
	if err != nil {
		t.Fatalf("list over cap: %v", err)
	}
	if !truncated || len(files) != capN {
		t.Fatalf("over cap: len=%d truncated=%v, want %d and true", len(files), truncated, capN)
	}
}

func writeEmptyFiles(t *testing.T, dir string, n int) {
	t.Helper()
	for i := range n {
		name := fmt.Sprintf("f%04d", i)
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
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
