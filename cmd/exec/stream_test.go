//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"
)

func TestStreamUploadAndDownload(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.txt")
	content := "hello stream payload data"
	size := int64(len(content))

	// 1. Test Stream Upload
	uploadReq := execution.NewExecuteRequest(execution.ActionFileUpload, "exec-up",
		execution.WithTargetUser("nobody"),
		execution.WithPayload(&payload.FileUpload{
			DstPath: filePath,
			Size:    &size,
		}),
	)

	body := strings.NewReader(content)
	code := runStreamUpload(context.Background(), uploadReq, body)
	if code != 0 {
		t.Fatalf("runStreamUpload failed with exit code %d", code)
	}

	readBytes, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read uploaded file: %v", err)
	}
	if string(readBytes) != content {
		t.Fatalf("uploaded content = %q, want %q", string(readBytes), content)
	}

	// 1.1 Test Stream Upload Rejects Nil Size
	nilSizeUploadReq := execution.NewExecuteRequest(execution.ActionFileUpload, "exec-up-nil",
		execution.WithTargetUser("nobody"),
		execution.WithPayload(&payload.FileUpload{
			DstPath: filepath.Join(tempDir, "nil.txt"),
			Size:    nil,
		}),
	)
	if code := runStreamUpload(context.Background(), nilSizeUploadReq, strings.NewReader("data")); code == 0 {
		t.Fatalf("runStreamUpload with nil size expected exit code != 0, got %d", code)
	}

	// 2. Test Stream Download validation
	downloadReq := execution.NewExecuteRequest(execution.ActionFileDownload, "exec-down",
		execution.WithTargetUser("nobody"),
		execution.WithPayload(&payload.FileDownload{
			SrcPath: filePath,
			Size:    &size,
		}),
	)

	code = runStreamDownload(context.Background(), downloadReq)
	if code != 0 {
		t.Fatalf("runStreamDownload failed with exit code %d", code)
	}
}
