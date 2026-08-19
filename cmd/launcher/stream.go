//go:build !windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/launcher/action"
)

// runStreamUpload implements FILE_UPLOAD.
//
// After the JSON header has been consumed by readRequest, the remaining bytes
// on stdin (body) are the raw file content.  This function is invoked by run()
// after all L1–L3 checks have already passed.
//
// Protocol:
//
//	stdin  → raw file bytes (remainder after JSON header)
//	stdout → {"success":true,"output":"..."}  or  {"success":false,"error":"..."}
func runStreamUpload(ctx context.Context, req *execution.ExecuteRequest, body io.Reader) int {
	p, err := execution.GetPayload[payload.FileUpload](req)
	if err != nil {
		respondError(fmt.Sprintf("invalid payload: %v", err))
		return 1
	}
	if p.DstPath == "" {
		respondError("dst_path is required")
		return 1
	}
	if err := action.ValidatePath("dst_path", p.DstPath); err != nil {
		respondError(fmt.Sprintf("invalid dst_path: %v", err))
		return 1
	}

	if p.Size == nil {
		respondError("size is required")
		return 1
	}
	size := *p.Size

	// Write body → dstPath atomically via a temp file.
	dstDir := filepath.Dir(p.DstPath)
	dstBase := filepath.Base(p.DstPath)
	f, err := os.CreateTemp(dstDir, "."+dstBase+".*.dce.tmp")
	if err != nil {
		respondError(fmt.Sprintf("failed to create temp file for %s: %v", p.DstPath, err))
		return 1
	}
	tmpPath := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	var written int64
	if size >= 0 {
		written, err = io.CopyN(f, newCtxReader(ctx, body), size)
		if err != nil {
			_ = f.Close()
			if ctx.Err() != nil {
				respondError(fmt.Sprintf("upload timed out after writing %d bytes", size))
				return 1
			}
			respondError(fmt.Sprintf("failed to write %d bytes: %v", size, err))
			return 1
		}
	} else {
		written, err = io.Copy(f, newCtxReader(ctx, body))
		if err != nil {
			_ = f.Close()
			if ctx.Err() != nil {
				respondError("upload timed out")
				return 1
			}
			respondError(fmt.Sprintf("failed to write data: %v", err))
			return 1
		}
	}
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		respondError(fmt.Sprintf("failed to chmod temp file: %v", err))
		return 1
	}
	if err := f.Close(); err != nil {
		respondError(fmt.Sprintf("failed to close temp file: %v", err))
		return 1
	}
	if err := os.Rename(tmpPath, p.DstPath); err != nil {
		respondError(fmt.Sprintf("failed to move file into place: %v", err))
		return 1
	}
	cleanup = false

	respondSuccessWithWritten("uploaded", written)
	return 0
}

// runStreamDownload implements FILE_DOWNLOAD.
//
// Protocol:
//
//	stdout → raw file bytes (piped directly into HTTP response by Engine)
//	stderr → {"success":false,"error":"..."} on failure only
func runStreamDownload(ctx context.Context, req *execution.ExecuteRequest) int {
	p, err := execution.GetPayload[payload.FileDownload](req)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, `{"success":false,"error":%q}`, fmt.Sprintf("invalid payload: %v", err))
		return 1
	}
	if p.SrcPath == "" {
		_, _ = fmt.Fprintf(os.Stderr, `{"success":false,"error":"src_path is required"}`)
		return 1
	}
	if err := action.ValidatePath("src_path", p.SrcPath); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, `{"success":false,"error":%q}`, fmt.Sprintf("invalid src_path: %v", err))
		return 1
	}

	f, err := os.Open(p.SrcPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, `{"success":false,"error":%q}`, fmt.Sprintf("failed to open %s: %v", p.SrcPath, err))
		return 1
	}
	defer func(f *os.File) { _ = f.Close() }(f)

	if p.Size != nil && *p.Size >= 0 {
		size := *p.Size
		if _, err := io.CopyN(os.Stdout, newCtxReader(ctx, f), size); err != nil {
			if ctx.Err() != nil {
				_, _ = fmt.Fprintf(os.Stderr, `{"success":false,"error":%q}`, fmt.Sprintf("download timed out after streaming %d bytes", size))
				return 1
			}
			_, _ = fmt.Fprintf(os.Stderr, `{"success":false,"error":%q}`, fmt.Sprintf("failed to stream %d bytes: %v", size, err))
			return 1
		}
		return 0
	}

	if _, err := io.Copy(os.Stdout, newCtxReader(ctx, f)); err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintf(os.Stderr, `{"success":false,"error":"download timed out"}`)
			return 1
		}
		_, _ = fmt.Fprintf(os.Stderr, `{"success":false,"error":%q}`, fmt.Sprintf("failed to stream file: %v", err))
		return 1
	}
	return 0
}

// newCtxReader wraps an io.Reader so that each Read call first checks the
// context deadline, preventing blocked I/O from hanging the launcher past
// the action timeout.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// newCtxReader creates a context-aware reader wrapper.
func newCtxReader(ctx context.Context, r io.Reader) *ctxReader {
	return &ctxReader{ctx: ctx, r: r}
}
