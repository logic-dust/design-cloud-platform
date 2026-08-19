package file

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"

	"design-cloud-engine/internal/execution/payload"

	commonfs "design-cloud-engine/internal/common/fs"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/execution"
)

// StreamExecutor directly forks dc-launcher for large file I/O, bypassing the
// gRPC/Agent chain. This is the Cockpit-style pipe pattern:
//
//	Upload:   HTTP body → Engine → stdin pipe → dc-launcher (as user) → user file
//	Download: user file → dc-launcher (as user) → stdout pipe → Engine → HTTP body
//
// No file content ever lives in Engine process memory.
//
// Framed stdin protocol (unified for all actions):
//
//	[compact JSON of execution.ExecuteInput]\n
//	[raw file bytes – upload only; absent for download]
type StreamExecutor struct {
	getLauncherPath func(context.Context) string
	tokenFile       string
}

// newStreamExecutor creates a StreamExecutor with the given launcher binary path resolver.
func newStreamExecutor(getLauncherPath func(context.Context) string) *StreamExecutor {
	return &StreamExecutor{
		getLauncherPath: getLauncherPath,
		tokenFile:       commonfs.LauncherTokenPath(),
	}
}

// buildFramedStdin returns an io.Reader whose bytes are:
//
//	[compact JSON of execution.ExecuteInput]\n
//	[optional raw file bytes – upload only]
func (e *StreamExecutor) buildFramedStdin(req *execution.ExecuteRequest, body io.Reader) (io.Reader, error) {
	token, err := e.readToken()
	if err != nil {
		return nil, err
	}

	input := execution.ExecuteInput{
		Version: config.VERSION,
		Token:   token,
		Request: req,
	}
	headerJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	readers := []io.Reader{
		strings.NewReader(string(headerJSON) + "\n"),
	}
	if body != nil {
		readers = append(readers, body)
	}
	return io.MultiReader(readers...), nil
}

// Execute runs a file action directly via local dc-launcher.
func (e *StreamExecutor) Execute(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	launcherBin := e.getLauncherPath(ctx)
	if launcherBin == "" {
		return "", fmt.Errorf("stream executor failed: launcher path is not configured")
	}

	stdin, err := e.buildFramedStdin(req, nil)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, launcherBin)
	cmd.Env = execution.BuildLauncherEnv(nil)
	cmd.Stdin = stdin
	cmd.WaitDelay = time.Second

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if errors.Is(runErr, exec.ErrWaitDelay) {
		runErr = nil
	}
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("launcher interrupted: %w", ctxErr)
		}
		if execution.LauncherKilledBySeccomp(runErr) {
			return "", fmt.Errorf("launcher killed by seccomp: blocked system call (SIGSYS)")
		}
	}

	var out execution.ExecuteOutput
	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		if runErr != nil {
			errMsg := cmp.Or(strings.TrimSpace(stderr.String()), runErr.Error())
			return "", fmt.Errorf("launcher execution failed: %s", errMsg)
		}
		return "", nil
	}

	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		if runErr != nil {
			errMsg := cmp.Or(strings.TrimSpace(stderr.String()), runErr.Error())
			return "", fmt.Errorf("launcher execution failed: %s", errMsg)
		}
		return raw, nil
	}

	if !out.Success {
		errMsg := cmp.Or(out.Error, strings.TrimSpace(stderr.String()), "launcher command failed")
		return "", &execution.ActionError{
			Code:        execution.ErrCodeCommandFailed,
			Message:     errMsg,
			ExecutionID: req.ExecutionID,
			ActionName:  req.ActionName,
			Stderr:      stderr.String(),
		}
	}

	return out.Output, nil
}

// Upload streams r directly into dc-launcher stdin; dc-launcher (running as
// username) writes to dstPath atomically.
func (e *StreamExecutor) Upload(ctx context.Context, r io.Reader, username, dstPath string, size int64) error {
	launcherBin := e.getLauncherPath(ctx)
	if launcherBin == "" {
		return fmt.Errorf("stream upload failed: launcher path is not configured")
	}

	req := execution.NewExecuteRequest(
		execution.ActionFileUpload,
		"",
		execution.WithTargetUser(username),
		execution.WithPayload(&payload.FileUpload{DstPath: dstPath, Size: &size}),
	)

	stdin, err := e.buildFramedStdin(req, r)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, launcherBin)
	cmd.Env = execution.BuildLauncherEnv(nil)
	cmd.Stdin = stdin
	cmd.WaitDelay = time.Second

	// Capture both stdout (success JSON) and stderr for error messages.
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("stream upload failed: launcher interrupted: %w", ctxErr)
		}
		if execution.LauncherKilledBySeccomp(err) {
			return fmt.Errorf("stream upload failed: launcher killed by seccomp: blocked system call (SIGSYS)")
		}
		errMsg := cmp.Or(strings.TrimSpace(stderr.String()), strings.TrimSpace(stdout.String()))
		return fmt.Errorf("stream upload failed: %w (launcher output: %s)", err, errMsg)
	}

	// On success, parse launcher stdout for the actual written byte count
	// and compare with the expected size for Content-Length mismatch detection.
	if size >= 0 {
		if out := parseUploadResult(stdout.String()); out != nil && out.Written != size {
			return fmt.Errorf("stream upload size mismatch: declared %d bytes, written %d bytes", size, out.Written)
		}
	}
	return nil
}

// Download streams the content of srcPath (read as username) directly into w.
// w is typically a http.ResponseWriter; headers must be set before calling this.
func (e *StreamExecutor) Download(ctx context.Context, w io.Writer, username, srcPath string, size int64) (int64, error) {
	launcherBin := e.getLauncherPath(ctx)
	if launcherBin == "" {
		return 0, fmt.Errorf("stream download failed: launcher path is not configured")
	}

	req := execution.NewExecuteRequest(
		execution.ActionFileDownload,
		"",
		execution.WithTargetUser(username),
		execution.WithPayload(&payload.FileDownload{SrcPath: srcPath, Size: &size}),
	)

	stdin, err := e.buildFramedStdin(req, nil)
	if err != nil {
		return 0, err
	}

	cmd := exec.CommandContext(ctx, launcherBin)
	cmd.Env = execution.BuildLauncherEnv(nil)
	cmd.Stdin = stdin
	cmd.WaitDelay = time.Second

	// Capture stderr for error reporting (stdout is file data, not text).
	var stderr strings.Builder
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("stream download failed: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("stream download failed: start launcher: %w", err)
	}

	written, copyErr := copyDownloadStream(ctx, w, stdout, size)
	if copyErr != nil {
		_ = cmd.Process.Kill()
	}

	err = cmd.Wait()
	if copyErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return written, fmt.Errorf("stream download failed: launcher interrupted: %w", ctxErr)
		}
		return written, fmt.Errorf("stream download failed: copy launcher output: %w (launcher stderr: %s)", copyErr, strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return written, fmt.Errorf("stream download failed: launcher interrupted: %w", ctxErr)
		}
		if execution.LauncherKilledBySeccomp(err) {
			return written, fmt.Errorf("stream download failed: launcher killed by seccomp: blocked system call (SIGSYS)")
		}
		errMsg := strings.TrimSpace(stderr.String())
		return written, fmt.Errorf("stream download failed: %w (launcher stderr: %s)", err, errMsg)
	}
	return written, nil
}

// parseUploadResult extracts the ExecuteOutput from launcher stdout.
// Returns nil if parsing fails (best-effort; failures are logged upstream).
func parseUploadResult(raw string) *execution.ExecuteOutput {
	var out execution.ExecuteOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		return nil
	}
	return &out
}

func copyDownloadStream(ctx context.Context, dst io.Writer, src io.Reader, size int64) (int64, error) {
	buf := make([]byte, 32*1024)
	remaining := size
	var written int64

	for size < 0 || remaining > 0 {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		if size >= 0 && int64(len(buf)) > remaining {
			buf = buf[:remaining]
		}

		n, err := src.Read(buf)
		if n > 0 {
			wrote, writeErr := writeFull(dst, buf[:n])
			written += wrote
			if writeErr != nil {
				return written, writeErr
			}
			if size >= 0 {
				remaining -= int64(n)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if size < 0 || remaining == 0 {
					return written, nil
				}
				return written, io.ErrUnexpectedEOF
			}
			return written, err
		}
		if n == 0 {
			return written, io.ErrNoProgress
		}
	}
	return written, nil
}

func writeFull(dst io.Writer, p []byte) (int64, error) {
	var written int64
	for len(p) > 0 {
		n, err := dst.Write(p)
		if n > 0 {
			written += int64(n)
			p = p[n:]
		}
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrNoProgress
		}
	}
	return written, nil
}

// readToken loads the launcher token from the runtime token file.
func (e *StreamExecutor) readToken() (string, error) {
	data, err := os.ReadFile(e.tokenFile)
	if err != nil {
		return "", fmt.Errorf("launcher token unavailable: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("launcher token file is empty: %s", e.tokenFile)
	}
	return token, nil
}

// StreamDownload streams the file at srcPath directly to w using dc-launcher
// running as username and returns the number of bytes written to w.
func (s *Service) StreamDownload(ctx context.Context, w io.Writer, username, srcPath string, size int64) (int64, error) {
	return s.streamExec.Download(ctx, w, username, srcPath, size)
}

// StreamUpload writes the content from r directly to dstPath on the target host
// using dc-launcher running as username. Size is the declared Content-Length;
// a negative value indicates unknown chunked transfer encoding.
func (s *Service) StreamUpload(ctx context.Context, r io.Reader, username, dstPath string, size int64) error {
	if err := s.CheckUploadPermission(ctx, username); err != nil {
		return err
	}
	maxSizeBytes := s.defaultUploadMaxSizeBytes(ctx)
	if size > maxSizeBytes {
		return fmt.Errorf("%w, max size is %d bytes", ErrUploadTooLarge, maxSizeBytes)
	}
	if size < 0 {
		s.logger.Debug("chunked upload with unknown size, relying on runtime limit", "dst", dstPath, "limit", maxSizeBytes)
	}
	limited := newUploadLimitReader(r, maxSizeBytes)
	err := s.streamExec.Upload(ctx, limited, username, dstPath, size)
	if limited.TooLarge() {
		return fmt.Errorf("%w, max size is %d bytes", ErrUploadTooLarge, maxSizeBytes)
	}
	if limited.Truncated() {
		s.logger.Warn("upload body shorter than declared Content-Length; file may be truncated", "dst", dstPath, "declared", size)
	}
	return err
}

// StreamUploadLocation resolves the named file location and writes the content
// from r to that location via StreamUpload.
func (s *Service) StreamUploadLocation(ctx context.Context, r io.ReadCloser, username, locationName, relPath string, size int64) error {
	defer func() { _ = r.Close() }()

	if err := s.ensureWritableLocation(ctx, locationName, username); err != nil {
		return err
	}
	cleanRel, err := normalizeLocationPath(relPath)
	if err != nil {
		return err
	}
	if cleanRel == "" {
		return fmt.Errorf("%w: path is required", fs.ErrInvalid)
	}
	dstPath, err := s.ResolveLocationPath(ctx, locationName, cleanRel, username)
	if err != nil {
		return err
	}
	return s.StreamUpload(ctx, r, username, dstPath, size)
}
