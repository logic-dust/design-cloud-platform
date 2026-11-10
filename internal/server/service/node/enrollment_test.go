package node

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/push"
)

func newTestNodeService(t *testing.T) (*Service, *db.Repos, string) {
	t.Helper()

	dir := t.TempDir()
	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(dir, "node-bootstrap-test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := db.NewRepos(store)

	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  filepath.Join(dir, "audit.log"),
		SecurityLogPath: filepath.Join(dir, "security.log"),
		NodeID:          "test-control",
		Version:         "test",
	})
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Close() })

	svc := &Service{
		nodeRepo:  repos.Node,
		logger:    slog.Default(),
		cfg:       Config{NodeID: "test-control", CertDir: filepath.Join(dir, "certs", "cluster")},
		auditSvc:  auditSvc,
		publisher: push.NoopPublisher{},
	}
	if _, err := InitializeCA(svc.clusterCACertPath(), svc.clusterCAKeyPath(), true); err != nil {
		t.Fatalf("init cluster CA: %v", err)
	}
	return svc, repos, dir
}

func TestEnrollWithToken_AuthorizationBoundary(t *testing.T) {
	svc, repos, dir := newTestNodeService(t)
	ctx := context.Background()

	const nodeID = "win-bootstrap-01"
	_, issued, err := svc.RegisterNodeFromInput(ctx, ReqCreateNodeForm{
		NodeID:   nodeID,
		IsServer: false,
	})
	if err != nil {
		t.Fatalf("register node: %v", err)
	}
	if issued == nil || issued.Token == "" {
		t.Fatal("expected an auto-generated bootstrap token on create")
	}
	firstToken := issued.Token

	if _, err := svc.EnrollWithToken(ctx, nodeID, "not-the-token", ""); !errors.Is(err, ErrInvalidNodeToken) {
		t.Errorf("wrong token: got %v, want ErrInvalidNodeToken", err)
	}

	enrollment, err := svc.EnrollWithToken(ctx, nodeID, firstToken, "10.0.0.1:50051")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if enrollment.CertPEM == "" || enrollment.KeyPEM == "" || enrollment.CAPEM == "" {
		t.Fatal("bootstrap returned empty certificate materials")
	}

	bootstrapped, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if bootstrapped.CertFingerprint == "" {
		t.Error("cert fingerprint must be persisted after bootstrap")
	}
	if bootstrapped.EnrollmentTokenHash != "" {
		t.Error("enrollment token hash must be cleared after a successful enrollment")
	}
	if bootstrapped.RuntimeStatus() != model.NodeStatusUnset {
		t.Errorf("status = %v, want unset before first connect", bootstrapped.RuntimeStatus())
	}
	if !strings.HasSuffix(bootstrapped.CertSubject, ",O="+orgNode) {
		t.Errorf("cert subject = %q, want it to end with O=%s", bootstrapped.CertSubject, orgNode)
	}
	firstFingerprint := bootstrapped.CertFingerprint

	if _, err := svc.EnrollWithToken(ctx, nodeID, firstToken, ""); !errors.Is(err, ErrMissingNodeToken) {
		t.Errorf("reusing a consumed token: got %v, want ErrMissingNodeToken", err)
	}

	rotated, err := svc.GenerateToken(ctx, nodeID)
	if err != nil {
		t.Fatalf("rotate token: %v", err)
	}
	if rotated.Token == "" || rotated.Token == firstToken {
		t.Fatal("token rotation must produce a fresh token")
	}
	if _, err := svc.EnrollWithToken(ctx, nodeID, rotated.Token, ""); err != nil {
		t.Fatalf("re-enroll with rotated token: %v", err)
	}

	reborn, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("reload node after re-enroll: %v", err)
	}
	if reborn.CertFingerprint == firstFingerprint {
		t.Error("re-enroll must overwrite the previous certificate binding")
	}
	if reborn.EnrollmentTokenHash != "" {
		t.Error("enrollment token hash must be cleared after re-enroll")
	}

	securityLog, err := os.ReadFile(filepath.Join(dir, "security.log"))
	if err != nil {
		t.Fatalf("read security audit log: %v", err)
	}
	if !strings.Contains(string(securityLog), "NODE_REENROLL") {
		t.Error("re-enroll must be recorded as a NODE_REENROLL security event")
	}

	if _, err := svc.EnrollWithToken(ctx, "no-such-node", "any-token", ""); !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("unknown node: got %v, want ErrNodeNotFound", err)
	}
}

func TestNodeService_Update_ConnectionsDisabledAndMaxConnections(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()

	const nodeID = "node-update-test"
	_, _, err := svc.RegisterNodeFromInput(ctx, ReqCreateNodeForm{
		NodeID:   nodeID,
		IsServer: false,
	})
	if err != nil {
		t.Fatalf("register node: %v", err)
	}

	disable := true
	maxConn := uint(10)
	updated, err := svc.Update(ctx, nodeID, UpdateNodeRequest{
		ConnectionsDisabled: &disable,
		MaxConnections:      &maxConn,
	})
	if err != nil {
		t.Fatalf("update node: %v", err)
	}
	if !updated.ConnectionsDisabled {
		t.Errorf("expected ConnectionsDisabled=true after update, got %v", updated.ConnectionsDisabled)
	}
	if updated.MaxConnections != 10 {
		t.Errorf("expected MaxConnections=10 after update, got %d", updated.MaxConnections)
	}

	reloaded, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if !reloaded.ConnectionsDisabled || reloaded.MaxConnections != 10 {
		t.Fatalf("persisted values mismatch: disabled=%v max=%d", reloaded.ConnectionsDisabled, reloaded.MaxConnections)
	}
}

func TestMaintenanceExitRestoresOfflineWithoutHeartbeat(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()
	const nodeID = "maint-node"
	now := time.Now()
	if err := repos.Node.Create(ctx, &model.Node{
		NodeID:          nodeID,
		InMaintenance:   true,
		LastConnectedAt: now,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	exit := false
	if _, err := svc.Update(ctx, nodeID, UpdateNodeRequest{InMaintenance: &exit}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := repos.Node.GetByNodeID(ctx, nodeID)
	if got.InMaintenance || got.RuntimeStatus() != model.NodeStatusOffline {
		t.Fatalf("InMaintenance=%v RuntimeStatus=%v, want false/offline", got.InMaintenance, got.RuntimeStatus())
	}
}

func TestEnrollWithToken_PrecheckDoesNotPolluteCertLocks(t *testing.T) {
	svc, _, _ := newTestNodeService(t)
	ctx := context.Background()

	if _, err := svc.EnrollWithToken(ctx, "unknown-node-1", "bad-token", "127.0.0.1:52255"); err == nil {
		t.Fatal("expected error for unknown node")
	}

	if _, err := svc.RegisterNode(ctx, &model.Node{NodeID: "known-node", IsServer: false}); err != nil {
		t.Fatalf("register node: %v", err)
	}
	if _, err := svc.EnrollWithToken(ctx, "known-node", "wrong-token", "127.0.0.1:52255"); err == nil {
		t.Fatal("expected error for wrong token")
	}

	var lockCount int
	svc.nodeCertLocks.Range(func(_, _ any) bool {
		lockCount++
		return true
	})
	if lockCount != 0 {
		t.Fatalf("nodeCertLocks count = %d, want 0 after failed prechecks", lockCount)
	}
}
