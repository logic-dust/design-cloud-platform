package dispatch

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"

	"google.golang.org/grpc"
)

type fakeLeader struct {
	isLeader bool
	leaderID string
	epoch    uint64
}

func (s fakeLeader) IsLeader() bool   { return s.isLeader }
func (s fakeLeader) LeaderID() string { return s.leaderID }
func (s fakeLeader) LocalEpoch() uint64 {
	if !s.isLeader {
		return 0
	}
	return s.epoch
}
func (s fakeLeader) LeaderEpoch() uint64 { return s.epoch }

type fakeSyncServer struct {
	grpc.ServerStream
	sent []*pb.ServerCommand
}

func (s *fakeSyncServer) Send(command *pb.ServerCommand) error {
	s.sent = append(s.sent, command)
	return nil
}

func (s *fakeSyncServer) Recv() (*pb.AgentMessage, error) { return nil, io.EOF }
func (s *fakeSyncServer) Context() context.Context        { return context.Background() }

type failingForwarder struct{}

func (failingForwarder) ForwardDispatch(context.Context, string, uint64, *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	return nil, fmt.Errorf("forward failed")
}

func newTestRepos(t *testing.T) (*db.Store, *db.Repos) {
	t.Helper()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store, db.NewRepos(store)
}

func createNode(t *testing.T, repo *db.NodeRepo, nodeID string, status model.NodeStatus) {
	t.Helper()
	if err := repo.Create(context.Background(), &model.Node{
		NodeID: nodeID,
		Status: status,
	}); err != nil {
		t.Fatalf("create node %s: %v", nodeID, err)
	}
}
