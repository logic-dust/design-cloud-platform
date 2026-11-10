package controlplane

import (
	"context"
	"log/slog"
	"testing"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/execution"
	controldispatch "design-cloud-engine/internal/server/controlplane/dispatch"
	agentregistry "design-cloud-engine/internal/server/controlplane/registry"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeLeaderState 实现 leader.Reader，用于构造双 Leader / 脑裂场景。
type fakeLeaderState struct {
	isLeader    bool
	leaderID    string
	localEpoch  uint64
	leaderEpoch uint64
}

func (f fakeLeaderState) IsLeader() bool   { return f.isLeader }
func (f fakeLeaderState) LeaderID() string { return f.leaderID }
func (f fakeLeaderState) LocalEpoch() uint64 {
	if !f.isLeader {
		return 0
	}
	return f.localEpoch
}
func (f fakeLeaderState) LeaderEpoch() uint64 { return f.leaderEpoch }

// fakePublisher 统计 DeliverRemoteEvent 调用次数，用于事件去重断言。
type fakePublisher struct {
	deliveries int
}

func (f *fakePublisher) PublishNodeEvent(context.Context, int, string, string, string, string, string) {
}
func (f *fakePublisher) DeliverRemoteEvent(context.Context, string, []string, int, string, string) {
	f.deliveries++
}

func newForwardTestServer(t *testing.T, leaderState fakeLeaderState) *Server {
	t.Helper()

	_, repos := newTestRepos(t)
	registry := agentregistry.New(slog.Default())
	t.Cleanup(registry.Stop)
	dispatcher := controldispatch.New(
		registry, leaderState, slog.Default(), nil, "server-a", repos.Node, repos.Execution)
	return &Server{
		Runtime: &Runtime{
			registry:   registry,
			logger:     slog.Default(),
			leader:     leaderState,
			nodeID:     "server-a",
			dispatcher: dispatcher,
			executions: repos.Execution,
			eventDedup: newEventDedup(),
		},
	}
}

func newValidForwardRequest(t *testing.T, leaderEpoch uint64) *pb.ForwardDispatchRequest {
	t.Helper()

	req, err := controldispatch.ToForwardRequest("server-b", leaderEpoch, &execution.ExecuteRequest{
		TraceID:     "trace-fence",
		ExecutionID: "exec-fence",
		ActionName:  execution.ActionUserList,
		TargetUser:  "alice",
		Timeout:     30,
	})
	if err != nil {
		t.Fatalf("build forward request: %v", err)
	}
	return req
}

func deadlineContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// 脑裂防护：Standby 持有过期的 Leader 视图（旧 epoch）转发请求时，
// 新 Leader 必须以 FailedPrecondition 拒绝，由客户端刷新 Leader 视图后重试。
func TestForwardDispatchRejectsStaleLeaderEpoch(t *testing.T) {
	t.Parallel()

	server := newForwardTestServer(t, fakeLeaderState{isLeader: true, leaderID: "server-a", localEpoch: 5})

	ctx, cancel := deadlineContext(t)
	defer cancel()
	_, err := server.ForwardDispatch(ctx, newValidForwardRequest(t, 4))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale epoch error = %v, want FailedPrecondition", err)
	}
}

func TestForwardDispatchAcceptsMatchingLeaderEpoch(t *testing.T) {
	t.Parallel()

	server := newForwardTestServer(t, fakeLeaderState{isLeader: true, leaderID: "server-a", localEpoch: 5})

	ctx, cancel := deadlineContext(t)
	defer cancel()
	// epoch 匹配后请求进入正常派发路径；测试环境无在线 Agent，失败是预期，
	// 但错误不得是 fencing 相关的 FailedPrecondition。
	_, err := server.ForwardDispatch(ctx, newValidForwardRequest(t, 5))
	if status.Code(err) == codes.FailedPrecondition {
		t.Fatalf("matching epoch rejected with FailedPrecondition: %v", err)
	}
}

func TestForwardDispatchRejectsNonLeader(t *testing.T) {
	t.Parallel()

	server := newForwardTestServer(t, fakeLeaderState{isLeader: false, leaderID: "server-b", leaderEpoch: 7})

	ctx, cancel := deadlineContext(t)
	defer cancel()
	_, err := server.ForwardDispatch(ctx, newValidForwardRequest(t, 7))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("non-leader error = %v, want FailedPrecondition", err)
	}
}

func TestForwardDispatchRequiresDeadline(t *testing.T) {
	t.Parallel()

	server := newForwardTestServer(t, fakeLeaderState{isLeader: true, leaderID: "server-a", localEpoch: 5})

	_, err := server.ForwardDispatch(context.Background(), newValidForwardRequest(t, 5))
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no-deadline error = %v, want InvalidArgument", err)
	}
}

func TestForwardDispatchRejectsHopLimit(t *testing.T) {
	t.Parallel()

	server := newForwardTestServer(t, fakeLeaderState{isLeader: true, leaderID: "server-a", localEpoch: 5})

	req := newValidForwardRequest(t, 5)
	req.Hop = 1
	ctx, cancel := deadlineContext(t)
	defer cancel()
	_, err := server.ForwardDispatch(ctx, req)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("hop-limit error = %v, want FailedPrecondition", err)
	}
}

func TestForwardDispatchAllowsZeroHop(t *testing.T) {
	t.Parallel()

	server := newForwardTestServer(t, fakeLeaderState{isLeader: true, leaderID: "server-a", localEpoch: 5})

	req := newValidForwardRequest(t, 5)
	req.Hop = 0
	ctx, cancel := deadlineContext(t)
	defer cancel()
	_, err := server.ForwardDispatch(ctx, req)
	if status.Code(err) == codes.FailedPrecondition {
		t.Fatalf("hop 0 rejected with FailedPrecondition: %v", err)
	}
}

// 双路径重复送达（变更节点直发 + Leader 二次扇出 / 关键事件失败重试）时，
// 同一 event_id 只能被处理一次。
func TestForwardClusterEventDeduplicates(t *testing.T) {
	t.Parallel()

	server := newForwardTestServer(t, fakeLeaderState{})
	publisher := &fakePublisher{}
	server.deps.Publisher = publisher

	event := &pb.ForwardClusterEventRequest{
		SourceNodeId: "server-b",
		EventId:      "event-dup-1",
		ExpiresAt:    time.Now().Add(ClusterEventDefaultTTL).Unix(),
		TargetType:   "broadcast",
		EventName:    "settings:updated",
		PayloadJson:  "{}",
	}
	if _, err := server.ForwardClusterEvent(context.Background(), event); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	// 重发同一 event_id（模拟重试/双路径）；接收侧不得再次投递。
	dup := &pb.ForwardClusterEventRequest{
		SourceNodeId: "server-b",
		EventId:      "event-dup-1",
		ExpiresAt:    event.ExpiresAt,
		TargetType:   "broadcast",
		EventName:    "settings:updated",
		PayloadJson:  "{}",
	}
	if _, err := server.ForwardClusterEvent(context.Background(), dup); err != nil {
		t.Fatalf("duplicate delivery: %v", err)
	}
	if publisher.deliveries != 1 {
		t.Fatalf("deliveries = %d, want 1 (duplicate event_id dropped)", publisher.deliveries)
	}
}

func TestForwardClusterEventDropsExpired(t *testing.T) {
	t.Parallel()

	server := newForwardTestServer(t, fakeLeaderState{})
	publisher := &fakePublisher{}
	server.deps.Publisher = publisher

	event := &pb.ForwardClusterEventRequest{
		SourceNodeId: "server-b",
		EventId:      "event-expired-1",
		ExpiresAt:    time.Now().Add(-time.Minute).Unix(),
		TargetType:   "broadcast",
		EventName:    "settings:updated",
		PayloadJson:  "{}",
	}
	resp, err := server.ForwardClusterEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("expired event: %v", err)
	}
	if !resp.GetSuccess() {
		t.Fatalf("expired event response = %+v, want success (drop is not an error)", resp)
	}
	if publisher.deliveries != 0 {
		t.Fatalf("deliveries = %d, want 0 (expired event dropped)", publisher.deliveries)
	}
}
