package audit

import (
	"cmp"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"design-cloud-engine/internal/platform/logger"
)

type auditLogger struct {
	logger     *slog.Logger
	closer     io.Closer
	logPath    string
	mu         sync.Mutex
	eventCount int64
	closed     bool
}

// sensitiveDetailKeys 是审计日志脱敏时需被遮盖的字段名（不区分大小写）。
var sensitiveDetailKeys = map[string]bool{
	"password": true, "passwd": true, "secret": true, "token": true,
	"apikey": true, "api_key": true, "apisecret": true, "api_secret": true,
	"accesskey": true, "access_key": true, "privatekey": true, "private_key": true,
	"credential": true, "credentials": true, "auth": true,
}

// sanitizeDetails 返回 details 的一份浅拷贝，其中敏感字段的值被替换为 "***"。
func sanitizeDetails(details map[string]any) map[string]any {
	if details == nil {
		return nil
	}
	out := make(map[string]any, len(details))
	for k, v := range details {
		if sensitiveDetailKeys[strings.ToLower(k)] {
			out[k] = "***"
		} else {
			out[k] = v
		}
	}
	return out
}

func newLogger(logPath string, nodeID string) (*auditLogger, error) {
	slogger, closer, err := logger.NewLogger(logger.LogConfig{
		Path:  logPath,
		Host:  nodeID,
		Level: slog.LevelInfo,
	})
	if err != nil {
		return nil, err
	}

	log := &auditLogger{
		logger:  slogger,
		closer:  closer,
		logPath: logPath,
	}

	log.LogSystemEvent(context.Background(), "AUDIT_SYSTEM_START", slog.LevelDebug, nil)

	return log, nil
}

func (al *auditLogger) logDomainEvent(ctx context.Context, evt event) {
	al.logEvent(ctx, evt)
}

func (al *auditLogger) logAuthEvent(ctx context.Context, action, result, userID, remoteIP string, level slog.Level, details map[string]any, duration time.Duration) {
	al.logDomainEvent(ctx, event{
		Level:     level,
		EventType: eventTypeAuth,
		Component: "auth",
		Action:    action,
		Result:    result,
		UserID:    userID,
		RemoteIP:  remoteIP,
		Details:   details,
		Duration:  duration.Milliseconds(),
	})
}

func (al *auditLogger) LogExecutionEvent(ctx context.Context, action, result, userID, traceID string, level slog.Level, details map[string]any, duration time.Duration) {
	al.logDomainEvent(ctx, event{
		Level:     level,
		EventType: eventTypeExecution,
		Component: "execution",
		Action:    action,
		Result:    result,
		UserID:    userID,
		TraceID:   traceID,
		Details:   details,
		Duration:  duration.Milliseconds(),
	})
}

func (al *auditLogger) LogSecurityEvent(ctx context.Context, action, result string, level slog.Level, details map[string]any) {
	al.logDomainEvent(ctx, event{
		Level:     level,
		EventType: eventTypeSecurity,
		Component: "security",
		Action:    action,
		Result:    result,
		Details:   details,
	})
}

func (al *auditLogger) LogSystemEvent(ctx context.Context, action string, level slog.Level, details map[string]any) {
	al.logDomainEvent(ctx, event{
		Level:     level,
		EventType: eventTypeSystem,
		Component: "system",
		Action:    action,
		Result:    "completed",
		Details:   details,
	})
}

func (al *auditLogger) LogUserEvent(ctx context.Context, action, result, userID string, level slog.Level, details map[string]any) {
	al.logDomainEvent(ctx, event{
		Level:     level,
		EventType: eventTypeUser,
		Component: "user",
		Action:    action,
		Result:    result,
		UserID:    userID,
		Details:   details,
	})
}

func (al *auditLogger) LogNodeEvent(ctx context.Context, action, result, nodeID string, level slog.Level, details map[string]any) {
	al.logDomainEvent(ctx, event{
		Level:     level,
		EventType: eventTypeNode,
		Component: "node",
		Action:    action,
		Result:    result,
		UserID:    nodeID,
		Details:   details,
	})
}

func (al *auditLogger) logEvent(ctx context.Context, event event) {
	al.mu.Lock()
	defer al.mu.Unlock()
	if al.closed {
		return
	}
	al.writeEvent(ctx, event)
	al.eventCount++
}

func (al *auditLogger) writeEvent(ctx context.Context, event event) {
	event.TraceID = cmp.Or(event.TraceID, extractTraceID(ctx))
	event.SessionID = cmp.Or(event.SessionID, extractSessionID(ctx))

	// 脱敏 details 中的敏感字段（如 password/token/secret）防止泄露到审计日志。
	sanitized := sanitizeDetails(event.Details)

	al.logger.Log(ctx, event.Level, event.Action,
		"event_type", event.EventType,
		"component", event.Component,
		"result", event.Result,
		"user_id", event.UserID,
		"session_id", event.SessionID,
		"remote_ip", event.RemoteIP,
		"duration_ms", event.Duration,
		"trace_id", event.TraceID,
		"risk_level", calculateRiskLevel(event.Action),
		"details", sanitized,
	)

}

func (al *auditLogger) Close() error {
	al.mu.Lock()
	defer al.mu.Unlock()
	if al.closed {
		return nil
	}
	al.writeEvent(context.Background(), event{
		Level:     slog.LevelInfo,
		EventType: eventTypeSystem,
		Component: "system",
		Action:    "AUDIT_SYSTEM_STOP",
		Result:    "completed",
		Details:   map[string]any{"total_events": al.eventCount},
	})
	al.closed = true
	if al.closer != nil {
		return al.closer.Close()
	}
	return nil
}

// extractTraceID extracts trace ID from context
var extractTraceID = func(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(TraceIDKey).(string); ok {
		return v
	}
	return ""
}

// extractSessionID extracts session ID from context
var extractSessionID = func(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(SessionIDKey).(string); ok {
		return v
	}
	return ""
}

// calculateRiskLevel determines risk level based on action type
func calculateRiskLevel(action string) string {
	upperAction := strings.ToUpper(action)
	switch {
	case strings.Contains(upperAction, "_DELETE") || strings.Contains(upperAction, "_REMOVE") || strings.Contains(upperAction, "_REVOKE"):
		return "High"
	case strings.Contains(upperAction, "_UPDATE") || strings.Contains(upperAction, "_CHANGE") || strings.Contains(upperAction, "_CREATE") || strings.Contains(upperAction, "_INIT") || strings.Contains(upperAction, "_ISSUE"):
		return "Medium"
	default:
		return "Low"
	}
}
