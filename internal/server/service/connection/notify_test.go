package connection

import (
	"context"
	"testing"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/push"
)

type recordingPublisher struct {
	msgs []push.Message
}

func (p *recordingPublisher) PublishToUser(_ context.Context, _ []string, msg push.Message) {
	p.msgs = append(p.msgs, msg)
}
func (p *recordingPublisher) PublishToRole(context.Context, model.Role, push.Message) {}
func (p *recordingPublisher) Broadcast(_ context.Context, msg push.Message) {
	p.msgs = append(p.msgs, msg)
}
func (p *recordingPublisher) PublishNodeEvent(context.Context, int, string, string, string, string, string) {
}
func (p *recordingPublisher) DeliverRemoteEvent(context.Context, string, []string, int, string, string) {
}

func TestNotifyOwnerRefresh_EmptyTargetWhenConnIDZero(t *testing.T) {
	pub := &recordingPublisher{}
	svc := &Service{publisher: pub}
	svc.notifyOwnerRefresh(context.Background(), "", "alice", 0)
	if len(pub.msgs) != 1 {
		t.Fatalf("got %d messages", len(pub.msgs))
	}
	p0, ok := pub.msgs[0].Payload.(push.EventPayload)
	if !ok {
		t.Fatalf("payload type %T", pub.msgs[0].Payload)
	}
	if p0.TargetID != "" {
		t.Fatalf("TargetID = %q, want empty for full refresh", p0.TargetID)
	}
	svc.notifyOwnerRefresh(context.Background(), AllUsers, "alice", 42)
	p1, ok := pub.msgs[1].Payload.(push.EventPayload)
	if !ok {
		t.Fatalf("payload type %T", pub.msgs[1].Payload)
	}
	if p1.TargetID != "42" {
		t.Fatalf("TargetID = %q, want 42", p1.TargetID)
	}
}

func TestNotifyOwnerRefresh_SkipsSelfServiceOperator(t *testing.T) {
	pub := &recordingPublisher{}
	svc := &Service{publisher: pub}
	svc.notifyOwnerRefresh(context.Background(), "alice", "alice", 7)
	if len(pub.msgs) != 0 {
		t.Fatalf("self-service operator must not receive push; got %d", len(pub.msgs))
	}
}
