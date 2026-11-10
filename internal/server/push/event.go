// Package push provides server-sent event infrastructure: an in-memory Hub
// that routes event messages to connected SSE clients by user ID and role.
package push

import (
	"context"

	"design-cloud-engine/internal/model"
)

// EventName identifies the SSE event type (the `event:` field).
type EventName string

const (
	EventSession    EventName = "session"
	EventSystem     EventName = "system"
	EventJob        EventName = "job"
	EventTicket     EventName = "ticket"
	EventConnection EventName = "connection"
	EventNode       EventName = "node"
)

// Notification 是可选的通知提示（toast）。nil（不填充）表示纯数据刷新，
// 前端不应弹通知；填充表示需要向用户展示提示。
type Notification struct {
	Title   string `json:"title"`   // toast 标题
	Message string `json:"message"` // toast 内容
	Level   string `json:"level"`   // info | success | warning | error
}

type EventPayload struct {
	Action   string        `json:"action,omitempty"`   // refresh | logout | notify
	TargetID string        `json:"targetId,omitempty"` // entity ID for partial refresh
	Notify   *Notification `json:"notify,omitempty"`   // 有通知才填充；nil 则字段不输出
	Extra    any           `json:"extra,omitempty"`    // reserved extension dict
}

// Message is an event with its routing name and payload.
type Message struct {
	Name    EventName
	Payload any
}

// Publisher is the injection interface for business services that emit events.
// Services depend on this small interface (not *Hub) so unit tests can inject
// a NoopPublisher without a mock framework.
type Publisher interface {
	PublishToUser(ctx context.Context, userIDs []string, msg Message)
	PublishToRole(ctx context.Context, role model.Role, msg Message)
	Broadcast(ctx context.Context, msg Message)
}

// NoopPublisher is an empty implementation for unit tests and default wiring.
type NoopPublisher struct{}

func (NoopPublisher) PublishToUser(context.Context, []string, Message)   {}
func (NoopPublisher) PublishToRole(context.Context, model.Role, Message) {}
func (NoopPublisher) Broadcast(context.Context, Message)                 {}
func (NoopPublisher) PublishNodeEvent(context.Context, int, string, string, string, string, string) {
}
func (NoopPublisher) DeliverRemoteEvent(context.Context, string, []string, int, string, string) {
}
