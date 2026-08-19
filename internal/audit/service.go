package audit

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

type Config struct {
	PrimaryLogPath  string
	SecurityLogPath string
	NodeID          string
	Version         string
}

type Service struct {
	primaryLogger   *auditLogger
	securityLogger  *auditLogger
	executions      *db.ExecutionRepo // optional, attached via WithExecutionRepo
	logPath         string
	securityLogPath string
	enabled         bool
	mu              sync.RWMutex
}

func NewService(cfg Config) (*Service, error) {
	primaryLogger, err := newLogger(cfg.PrimaryLogPath, 100, 10, cfg.NodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to create primary audit logger: %w", err)
	}

	securityLogger, err := newLogger(cfg.SecurityLogPath, 50, 20, cfg.NodeID)
	if err != nil {
		_ = primaryLogger.Close()
		return nil, fmt.Errorf("failed to create security audit logger: %w", err)
	}

	svc := &Service{
		primaryLogger:   primaryLogger,
		securityLogger:  securityLogger,
		logPath:         cfg.PrimaryLogPath,
		securityLogPath: cfg.SecurityLogPath,
		enabled:         true,
	}

	svc.LogSystem(context.Background(), "AUDIT_SERVICE_INIT", slog.LevelInfo, nil)
	return svc, nil
}

func (s *Service) IsEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// WithExecutionRepo attaches the execution repository for execution-records
// audit queries. It is intentionally separate from NewService so the agent
// side can construct an audit service without dragging DB dependencies.
func (s *Service) WithExecutionRepo(repo *db.ExecutionRepo) *Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executions = repo
	return s
}

func (s *Service) withPrimaryLogger(fn func(*auditLogger)) {
	if !s.IsEnabled() || s.primaryLogger == nil {
		return
	}
	fn(s.primaryLogger)
}

func (s *Service) withSecurityLogger(fn func(*auditLogger)) {
	if !s.IsEnabled() || s.securityLogger == nil {
		return
	}
	fn(s.securityLogger)
}

func (s *Service) LogAuth(ctx context.Context, action, result, userID, remoteIP string, level slog.Level, details map[string]any, duration time.Duration) {
	s.withPrimaryLogger(func(log *auditLogger) {
		log.logAuthEvent(ctx, action, result, userID, remoteIP, level, details, duration)
	})
}

func (s *Service) LogExecution(ctx context.Context, action, result, userID, traceID string, level slog.Level, details map[string]any, duration time.Duration) {
	s.withPrimaryLogger(func(log *auditLogger) {
		log.LogExecutionEvent(ctx, action, result, userID, traceID, level, details, duration)
	})
}

func (s *Service) LogSecurity(ctx context.Context, action, result string, level slog.Level, details map[string]any) {
	s.withSecurityLogger(func(log *auditLogger) {
		log.LogSecurityEvent(ctx, action, result, level, details)
	})
}

func (s *Service) LogUser(ctx context.Context, action, result, userID string, level slog.Level, details map[string]any) {
	s.withPrimaryLogger(func(log *auditLogger) {
		log.LogUserEvent(ctx, action, result, userID, level, details)
	})
}

func (s *Service) LogNode(ctx context.Context, action, result, nodeID string, level slog.Level, details map[string]any) {
	s.withPrimaryLogger(func(log *auditLogger) {
		log.LogNodeEvent(ctx, action, result, nodeID, level, details)
	})
}

func (s *Service) LogSystem(ctx context.Context, action string, level slog.Level, details map[string]any) {
	s.withPrimaryLogger(func(log *auditLogger) {
		log.LogSystemEvent(ctx, action, level, details)
	})
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	if s.primaryLogger != nil {
		if err := s.primaryLogger.Close(); err != nil {
			errs = append(errs, fmt.Errorf("primary logger close error: %w", err))
		}
	}
	if s.securityLogger != nil {
		if err := s.securityLogger.Close(); err != nil {
			errs = append(errs, fmt.Errorf("security logger close error: %w", err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// LogTailResponse is the payload for GET /api/admin/audit/logs.
type LogTailResponse struct {
	Lines         []string  `json:"lines"`
	FileSizeBytes int64     `json:"fileSizeBytes"`
	ModifiedAt    time.Time `json:"modifiedAt"`
	Tail          int       `json:"tail"`
	Page          int       `json:"page"`
}

// ExecutionRecordView is the auditor-facing projection of an ExecutionRecord.
// The DTO keeps internal columns (e.g. system_error) exposed because auditors
// legitimately need them; the API is gated to AuditAdmin/SuperAdmin only.
type ExecutionRecordView struct {
	ExecutionID string                `json:"executionId"`
	TraceID     string                `json:"traceId"`
	ActionName  string                `json:"actionName"`
	ServerNode  string                `json:"serverNode"`
	TargetNode  string                `json:"targetNode,omitempty"`
	Status      model.ExecutionStatus `json:"status"`
	Success     bool                  `json:"success"`
	ExitCode    int                   `json:"exitCode"`
	SystemError string                `json:"systemError,omitempty"`
	FinishedAt  *time.Time            `json:"finishedAt,omitempty"`
	CreatedAt   time.Time             `json:"createdAt"`
	UpdatedAt   time.Time             `json:"updatedAt"`
}

// ExecutionListResult is the payload for GET /api/admin/audit/executions.
type ExecutionListResult struct {
	Items  []ExecutionRecordView `json:"items"`
	Total  int64                 `json:"total"`
	Limit  int                   `json:"limit"`
	Offset int                   `json:"offset"`
}

var ErrExecutionRepoNotAttached = fmt.Errorf("execution repository is not attached to audit service")

// ListExecutions returns execution records for audit purposes. It returns
// ErrExecutionRepoNotAttached when the service was constructed without an
// attached execution repository (e.g. on the agent side).
func (s *Service) ListExecutions(ctx context.Context, filter db.ExecutionListFilter) (*ExecutionListResult, error) {
	s.mu.RLock()
	repo := s.executions
	s.mu.RUnlock()
	if repo == nil {
		return nil, ErrExecutionRepoNotAttached
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	filter.Limit = limit

	records, total, err := repo.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list executions: %w", err)
	}

	views := make([]ExecutionRecordView, 0, len(records))
	for i := range records {
		views = append(views, toExecutionRecordView(&records[i]))
	}
	return &ExecutionListResult{
		Items:  views,
		Total:  total,
		Limit:  limit,
		Offset: filter.Offset,
	}, nil
}

// GetExecutionByExecutionID returns a single execution record by its
// execution_id. Returns ErrExecutionRepoNotAttached when no repository is
// attached. Returns the same wrapped common.ErrNotFound as the underlying repo.
func (s *Service) GetExecutionByExecutionID(ctx context.Context, executionID string) (*ExecutionRecordView, error) {
	s.mu.RLock()
	repo := s.executions
	s.mu.RUnlock()
	if repo == nil {
		return nil, ErrExecutionRepoNotAttached
	}

	record, err := repo.GetByExecutionID(ctx, executionID)
	if err != nil {
		return nil, err
	}
	return new(toExecutionRecordView(&record)), nil
}

func toExecutionRecordView(record *model.ExecutionRecord) ExecutionRecordView {
	return ExecutionRecordView{
		ExecutionID: record.ExecutionID,
		TraceID:     record.TraceID,
		ActionName:  record.ActionName,
		ServerNode:  record.ServerNode,
		TargetNode:  record.TargetNode,
		Status:      record.Status,
		Success:     record.Success,
		ExitCode:    record.ExitCode,
		SystemError: record.SystemError,
		FinishedAt:  record.FinishedAt,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
	}
}

const (
	// maxTailLines caps both the in-memory line buffer and the size of the
	// response serialized back to the client, i.e. it is also the transmission
	// bound for a single log-tail request.
	maxTailLines     = 500
	defaultTailLines = 200
)

// LogPath returns the current file path for the given log name (audit/security).
func (s *Service) LogPath(name string) (string, bool) {
	switch name {
	case "security":
		return s.securityLogPath, true
	case "audit":
		return s.logPath, true
	}
	return "", false
}

// ReadAuditLogTail reads the last `tail` lines of the audit or security log file at the given page.
func (s *Service) ReadAuditLogTail(name string, tail, page int) (LogTailResponse, error) {
	tail = min(tail, maxTailLines)
	tail = cmp.Or(tail, defaultTailLines)
	page = cmp.Or(page, 1)

	var path string
	if name == "security" {
		path = s.securityLogPath
	} else {
		path = s.logPath
	}

	fi, err := os.Stat(path)
	if err != nil {
		return LogTailResponse{}, fmt.Errorf("audit log file not accessible: %w", err)
	}

	lines, err := tailFile(path, tail, page)
	if err != nil {
		return LogTailResponse{}, err
	}

	return LogTailResponse{
		Lines:         lines,
		FileSizeBytes: fi.Size(),
		ModifiedAt:    fi.ModTime().UTC(),
		Tail:          tail,
		Page:          page,
	}, nil
}

const auditStreamPollInterval = 500 * time.Millisecond

// FollowLogTail streams new lines from the end of the specified audit or security log file.
// It starts from the end of the file (no history), polls for new lines, and
// reopens the file when rotation is detected (inode change).
func (s *Service) FollowLogTail(ctx context.Context, name string, onLine func(string) error) error {
	logPath, ok := s.LogPath(name)
	if !ok {
		return fmt.Errorf("unknown log: %s (valid: audit, security)", name)
	}

	f, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek log file: %w", err)
	}

	r := bufio.NewReader(f)
	ticker := time.NewTicker(auditStreamPollInterval)
	defer ticker.Stop()

	rewindIfRotated := func() error {
		stat, err := os.Stat(logPath)
		if err != nil {
			return err
		}
		fstat, err := f.Stat()
		if err != nil {
			return err
		}
		if os.SameFile(stat, fstat) {
			return nil
		}
		_ = f.Close()
		nf, err := os.Open(logPath)
		if err != nil {
			f = nil
			return err
		}
		if _, err := nf.Seek(0, io.SeekEnd); err != nil {
			_ = nf.Close()
			f = nil
			return err
		}
		f = nf
		r = bufio.NewReader(f)
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := rewindIfRotated(); err != nil {
				continue
			}
			for {
				line, err := r.ReadString('\n')
				trimmed := strings.TrimRight(line, "\r\n")
				if len(trimmed) > 0 {
					if err := onLine(trimmed); err != nil {
						return err
					}
				}
				if err != nil {
					break // io.EOF or read error: wait for next tick
				}
			}
		}
	}
}

// tailFile returns the requested block of lines from the end of the file.
//
// Small files are read entirely and paginated from the end (cheap, exact
// paging). Large files bound both I/O and memory: only the trailing chunk is
// scanned and at most n lines are kept, so a huge file combined with an
// unbounded page cannot exhaust memory.
func tailFile(path string, n, page int) ([]string, error) {
	f, err := os.Open(path) // #nosec G304 — path is to the internal audit log
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	defer func(f *os.File) { _ = f.Close() }(f)

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat log file: %w", err)
	}

	const maxFullReadBytes = 16 * 1024 * 1024 // 16 MiB: safe to read entirely
	if fi.Size() <= maxFullReadBytes {
		return paginateLines(f, n, page)
	}

	// Large file: read only the last chunk, then keep at most n lines.
	const chunkBytes = 8 * 1024 * 1024
	offset := fi.Size() - chunkBytes
	offset = max(offset, 0)
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek log file: %w", err)
	}
	// Discard a partial first line introduced by the seek.
	if offset > 0 {
		if _, err := bufio.NewReader(f).ReadString('\n'); err != nil && err != io.EOF {
			return nil, fmt.Errorf("skip partial line: %w", err)
		}
	}
	return readLastNLines(f, n)
}

// paginateLines reads all lines and returns the block [endIdx-n, endIdx)
// counted from the end, matching the original pagination semantics for small files.
func paginateLines(f *os.File, n, page int) ([]string, error) {
	all, err := scanLines(f)
	if err != nil {
		return nil, err
	}
	total := len(all)
	endIdx := total - (page-1)*n
	startIdx := max(endIdx-n, 0)
	if endIdx <= 0 {
		return []string{}, nil
	}
	result := make([]string, endIdx-startIdx)
	copy(result, all[startIdx:endIdx])
	return trimEmptyTrailing(result), nil
}

// readLastNLines scans f and returns at most the last n lines using a bounded
// ring buffer, capping memory regardless of file size.
func readLastNLines(f *os.File, n int) ([]string, error) {
	buf := make([]string, 0, n)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		buf = append(buf, scanner.Text())
		if len(buf) > n {
			buf = buf[len(buf)-n:]
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, fmt.Errorf("scan log file: %w", err)
	}
	return trimEmptyTrailing(buf), nil
}

func scanLines(f *os.File) ([]string, error) {
	var all []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		all = append(all, scanner.Text())
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, fmt.Errorf("scan log file: %w", err)
	}
	return all, nil
}

func trimEmptyTrailing(s []string) []string {
	for len(s) > 0 && s[len(s)-1] == "" {
		s = s[:len(s)-1]
	}
	return s
}
