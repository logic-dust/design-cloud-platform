package control

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"io"
	"log/slog"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

type fakeLeaderState struct {
	isLeader bool
	leaderID string
}

func (s fakeLeaderState) IsLeader() bool {
	return s.isLeader
}

func (s fakeLeaderState) LeaderID() string {
	return s.leaderID
}

type fakeSyncServer struct {
	grpc.ServerStream
	ctx      context.Context
	incoming []*pb.AgentMessage
	sent     []*pb.ServerCommand
	sendErr  error
	onSend   func(*pb.ServerCommand)
}

func (s *fakeSyncServer) Send(cmd *pb.ServerCommand) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	if s.onSend != nil {
		s.onSend(cmd)
	}
	s.sent = append(s.sent, cmd)
	return nil
}

func (s *fakeSyncServer) Recv() (*pb.AgentMessage, error) {
	if len(s.incoming) == 0 {
		return nil, io.EOF
	}
	msg := s.incoming[0]
	s.incoming = s.incoming[1:]
	return msg, nil
}

func (s *fakeSyncServer) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func peerContextWithCertificate(t *testing.T) (context.Context, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "agent-a"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	sum := sha256.Sum256(cert.Raw)
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
		}},
	})
	return ctx, hex.EncodeToString(sum[:])
}

type fakeConnectionSyncHandler struct {
	reconcileCalls int
	reconcileNode  string
	connections    []contract.ConnectionInfo
	rdpCalls       int
	rdpNode        string
	rdpStatus      *pb.RDPStatus
	err            error
}

func (h *fakeConnectionSyncHandler) SyncVNCConnections(_ context.Context, nodeID string, connections []contract.ConnectionInfo) error {
	if h.err != nil {
		return h.err
	}
	h.reconcileCalls++
	h.reconcileNode = nodeID
	h.connections = append([]contract.ConnectionInfo(nil), connections...)
	return nil
}

func (h *fakeConnectionSyncHandler) SyncStaticRDPConnection(_ context.Context, nodeID, _ string, rdp *pb.RDPStatus) error {
	if h.err != nil {
		return h.err
	}
	h.rdpCalls++
	h.rdpNode = nodeID
	h.rdpStatus = rdp
	return nil
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
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store, db.NewRepos(store)
}

func createNode(t *testing.T, repo *db.NodeRepo, nodeID string, status model.NodeStatus) {
	t.Helper()
	if err := repo.Create(context.Background(), &model.Node{
		NodeID:   nodeID,
		IsServer: false,
		Status:   status,
		Labels:   map[string]string{},
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}
}

func createRegisteredNode(t *testing.T, repo *db.NodeRepo, nodeID, fingerprint string) {
	t.Helper()
	if err := repo.Create(context.Background(), &model.Node{
		NodeID:          nodeID,
		IsServer:        false,
		Status:          model.NodeStatusOffline,
		Labels:          map[string]string{},
		CertFingerprint: fingerprint,
	}); err != nil {
		t.Fatalf("create registered node: %v", err)
	}
}
