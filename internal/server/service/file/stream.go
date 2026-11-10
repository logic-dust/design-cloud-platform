package file

import (
	"cmp"
	"context"
	"design-cloud-engine/internal/contract"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/platform/config"
	commonfs "design-cloud-engine/internal/platform/fs"
	"design-cloud-engine/internal/platform/process"
)

// StreamExecutor directly forks PrivilegedExec for large file I/O, bypassing the
// gRPC/Agent chain. This is the Cockpit-style pipe pattern:
//
//	Upload:   HTTP body → Engine → stdin pipe → PrivilegedExec (as user) → user file
//	Download: user file → PrivilegedExec (as user) → stdout pipe → Engine → HTTP body
//
// No file content ever lives in Engine process memory.
//
// Framed stdin protocol (unified for all actions):
//
//	[compact JSON of execution.ExecuteInput]\n
//	[raw file bytes – upload only; absent for download]
type StreamExecutor struct {
	getExecBinPath func(context.Context) string
	tokenFile      string
}

// newStreamExecutor creates a StreamExecutor with the given PrivilegedExec binary path resolver.
func newStreamExecutor(getExecBinPath func(context.Context) string) *StreamExecutor {
	return &StreamExecutor{
		getExecBinPath: getExecBinPath,
		tokenFile:      commonfs.DcExecTokenPath(),
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

const (
	// dc-execStderrLimit 限制 PrivilegedExec stderr 的捕获量：stderr 仅用于报错，前若干行已足够。
	dcexecStderrLimit = 64 << 10
	// dc-execAckStdoutLimit 限制上传等仅返回一行结果 JSON 的 stdout 捕获量。
	dcexecAckStdoutLimit = 64 << 10
	// dc-execResultStdoutLimit 限制 Execute 结果 JSON 的大小：文件读取上限 1 MiB（JSON 转义最坏约 6 倍），
	// 目录列表十万级条目约 15–20 MiB；超限直接报错，不做截断（截断的 JSON 无法解析）。
	dcexecResultStdoutLimit = 32 << 20
)

// ErrDcExecResultTooLarge 表示 PrivilegedExec 返回的结果超出 dc-execResultStdoutLimit（Portal 映射为 HTTP 413）。
var ErrDcExecResultTooLarge = fmt.Errorf("file action result exceeds %d MiB limit", dcexecResultStdoutLimit>>20)

// Execute runs a file action directly via local PrivilegedExec.
func (e *StreamExecutor) Execute(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	execBin := e.getExecBinPath(ctx)
	if execBin == "" {
		return "", fmt.Errorf("stream executor failed: PrivilegedExec path is not configured")
	}

	stdin, err := e.buildFramedStdin(req, nil)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, execBin)
	cmd.Env = execution.BuildDcExecEnv(nil)
	cmd.Stdin = stdin
	cmd.WaitDelay = time.Second

	stdout := process.NewBoundedBuffer(dcexecResultStdoutLimit)
	stderr := process.NewBoundedBuffer(dcexecStderrLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if errors.Is(runErr, exec.ErrWaitDelay) {
		runErr = nil
	}
	if stdout.Truncated() {
		return "", ErrDcExecResultTooLarge
	}
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("PrivilegedExec interrupted: %w", ctxErr)
		}
		if execution.DcExecKilledBySeccomp(runErr) {
			return "", fmt.Errorf("PrivilegedExec killed by seccomp: blocked system call (SIGSYS)")
		}
	}

	var out execution.ExecuteOutput
	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		if runErr != nil {
			errMsg := cmp.Or(strings.TrimSpace(stderr.String()), runErr.Error())
			return "", fmt.Errorf("PrivilegedExec execution failed: %s", errMsg)
		}
		return "", nil
	}

	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		if runErr != nil {
			errMsg := cmp.Or(strings.TrimSpace(stderr.String()), runErr.Error())
			return "", fmt.Errorf("PrivilegedExec execution failed: %s", errMsg)
		}
		return raw, nil
	}

	if !out.Success {
		errMsg := cmp.Or(out.Error, strings.TrimSpace(stderr.String()), "PrivilegedExec command failed")
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

// Upload streams r directly into PrivilegedExec stdin; PrivilegedExec (running as
// username) writes to dstPath atomically.
func (e *StreamExecutor) Upload(ctx context.Context, r io.Reader, username, dstPath string, size int64) error {
	execBin := e.getExecBinPath(ctx)
	if execBin == "" {
		return fmt.Errorf("stream upload failed: PrivilegedExec path is not configured")
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

	cmd := exec.CommandContext(ctx, execBin)
	cmd.Env = execution.BuildDcExecEnv(nil)
	cmd.Stdin = stdin
	cmd.WaitDelay = time.Second

	// Capture both stdout (success JSON) and stderr for error messages.
	stdout := process.NewBoundedBuffer(dcexecAckStdoutLimit)
	stderr := process.NewBoundedBuffer(dcexecStderrLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("stream upload failed: PrivilegedExec interrupted: %w", ctxErr)
		}
		if execution.DcExecKilledBySeccomp(err) {
			return fmt.Errorf("stream upload failed: PrivilegedExec killed by seccomp: blocked system call (SIGSYS)")
		}
		errMsg := cmp.Or(strings.TrimSpace(stderr.String()), strings.TrimSpace(stdout.String()))
		return fmt.Errorf("stream upload failed: %w (PrivilegedExec output: %s)", err, errMsg)
	}

	// On success, parse PrivilegedExec stdout for the actual written byte count
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
	execBin := e.getExecBinPath(ctx)
	if execBin == "" {
		return 0, fmt.Errorf("stream download failed: PrivilegedExec path is not configured")
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

	cmd := exec.CommandContext(ctx, execBin)
	cmd.Env = execution.BuildDcExecEnv(nil)
	cmd.Stdin = stdin
	cmd.WaitDelay = time.Second

	// Capture stderr for error reporting (stdout is file data, not text).
	stderr := process.NewBoundedBuffer(dcexecStderrLimit)
	cmd.Stderr = stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("stream download failed: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("stream download failed: start PrivilegedExec: %w", err)
	}

	written, copyErr := copyDownloadStream(ctx, w, stdout, size)
	if copyErr != nil {
		_ = cmd.Process.Kill()
	}

	err = cmd.Wait()
	if copyErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return written, fmt.Errorf("stream download failed: PrivilegedExec interrupted: %w", ctxErr)
		}
		return written, fmt.Errorf("stream download failed: copy PrivilegedExec output: %w (PrivilegedExec stderr: %s)", copyErr, strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return written, fmt.Errorf("stream download failed: PrivilegedExec interrupted: %w", ctxErr)
		}
		if execution.DcExecKilledBySeccomp(err) {
			return written, fmt.Errorf("stream download failed: PrivilegedExec killed by seccomp: blocked system call (SIGSYS)")
		}
		errMsg := strings.TrimSpace(stderr.String())
		return written, fmt.Errorf("stream download failed: %w (PrivilegedExec stderr: %s)", err, errMsg)
	}
	return written, nil
}

// parseUploadResult extracts the ExecuteOutput from PrivilegedExec stdout.
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

// readToken loads the PrivilegedExec token from the runtime token file.
func (e *StreamExecutor) readToken() (string, error) {
	data, err := os.ReadFile(e.tokenFile)
	if err != nil {
		return "", fmt.Errorf("PrivilegedExec token unavailable: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("PrivilegedExec token file is empty: %s", e.tokenFile)
	}
	return token, nil
}

// StreamDownload streams the file at srcPath directly to w using PrivilegedExec
// running as username and returns the number of bytes written to w.
func (s *Service) StreamDownload(ctx context.Context, w io.Writer, username, srcPath string, size int64) (int64, error) {
	return s.streamExec.Download(ctx, w, username, srcPath, size)
}

// StreamUpload writes the content from r directly to dstPath on the target host
// using PrivilegedExec running as username. Size is the declared Content-Length;
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
		return contract.NewInvalidParamError("path is required")
	}
	dstPath, err := s.ResolveLocationPath(ctx, locationName, cleanRel, username)
	if err != nil {
		return err
	}
	return s.StreamUpload(ctx, r, username, dstPath, size)
}
