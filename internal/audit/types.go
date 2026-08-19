package audit

import (
	"log/slog"
)

type eventType string

const (
	eventTypeAuth      eventType = "AUTH"
	eventTypeExecution eventType = "EXECUTION"
	eventTypeSecurity  eventType = "SECURITY"
	eventTypeUser      eventType = "USER"
	eventTypeNode      eventType = "NODE"
	eventTypeSystem    eventType = "SYSTEM"
)

type contextKey string

const (
	TraceIDKey   contextKey = "audit_trace_id"
	SessionIDKey contextKey = "audit_session_id"
)

type event struct {
	Level     slog.Level     `json:"-"`
	EventType eventType      `json:"eventType"`
	Component string         `json:"component"`
	Action    string         `json:"action"`
	Result    string         `json:"result"`
	UserID    string         `json:"userId,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
	RemoteIP  string         `json:"remoteIp,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	Duration  int64          `json:"duration,omitempty"`
	TraceID   string         `json:"traceId,omitempty"`
}
