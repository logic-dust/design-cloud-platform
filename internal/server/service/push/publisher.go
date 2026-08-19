package push

import (
	"context"

	"design-cloud-engine/internal/model"
)

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
