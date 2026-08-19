package node

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/push"
)

// newTestNodeService 构造一个带真实 SQLite 库、真实审计日志与真实 cluster CA 的
// node Service，使 BootstrapWithToken 能走完整的「签发证书 → 落盘 → 绑定指纹」链路。
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

// TestBootstrapWithToken_AuthorizationBoundary 锁定 OTP 引导的授权边界：
// 有效 token 即授权签发**或重新签发**；无 token / 错误 token 一律拒绝；
// 重新签发必须覆盖既有证书绑定（原地重装路径），且该动作要留审计痕迹。
func TestBootstrapWithToken_AuthorizationBoundary(t *testing.T) {
	svc, repos, dir := newTestNodeService(t)
	ctx := context.Background()

	const nodeID = "win-bootstrap-01"
	created, err := svc.RegisterNodeFromInput(ctx, ReqCreateNodeForm{
		NodeID:   nodeID,
		IsServer: false,
		Status:   model.NodeStatusPending,
	})
	if err != nil {
		t.Fatalf("register node: %v", err)
	}
	firstToken := created.Token
	if firstToken == "" {
		t.Fatal("expected an auto-generated bootstrap token on create")
	}

	// 1) 节点持有有效 token 时，错误 token 必须被拒（与“token 已用尽”区分开）。
	if _, err := svc.BootstrapWithToken(ctx, nodeID, "not-the-token", ""); !errors.Is(err, ErrInvalidNodeToken) {
		t.Errorf("wrong token: got %v, want ErrInvalidNodeToken", err)
	}

	// 2) 首次引导：签发成功、指纹落库、token 一次性清空、状态转 provisioned。
	materials, err := svc.BootstrapWithToken(ctx, nodeID, firstToken, "10.0.0.1:50051")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if materials.CertPEM == "" || materials.KeyPEM == "" || materials.CAPEM == "" {
		t.Fatal("bootstrap returned empty certificate materials")
	}

	bootstrapped, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if bootstrapped.CertFingerprint == "" {
		t.Error("cert fingerprint must be persisted after bootstrap")
	}
	if bootstrapped.Token != "" {
		t.Error("bootstrap token must be cleared after a successful bootstrap")
	}
	if bootstrapped.Status != model.NodeStatusProvisioned {
		t.Errorf("status = %v, want provisioned", bootstrapped.Status)
	}
	// 证书 Organization 必须是集群组织名，而不是签发方控制节点 ID。
	if !strings.HasSuffix(bootstrapped.CertSubject, ",O="+orgNode) {
		t.Errorf("cert subject = %q, want it to end with O=%s", bootstrapped.CertSubject, orgNode)
	}
	firstFingerprint := bootstrapped.CertFingerprint

	// 3) token 一次性：清空后同一 token 不可复用。
	if _, err := svc.BootstrapWithToken(ctx, nodeID, firstToken, ""); !errors.Is(err, ErrMissingNodeToken) {
		t.Errorf("reusing a consumed token: got %v, want ErrMissingNodeToken", err)
	}

	// 4) 重新签发 token 后可再次引导（原地重装/灾备路径），指纹被覆盖、token 再次清空。
	rotated, err := svc.GenerateToken(ctx, nodeID)
	if err != nil {
		t.Fatalf("rotate token: %v", err)
	}
	if rotated.Token == "" || rotated.Token == firstToken {
		t.Fatal("token rotation must produce a fresh token")
	}
	if _, err := svc.BootstrapWithToken(ctx, nodeID, rotated.Token, ""); err != nil {
		t.Fatalf("re-bootstrap with rotated token: %v", err)
	}

	reborn, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("reload node after re-bootstrap: %v", err)
	}
	if reborn.CertFingerprint == firstFingerprint {
		t.Error("re-bootstrap must overwrite the previous certificate binding")
	}
	if reborn.Token != "" {
		t.Error("bootstrap token must be cleared after re-bootstrap")
	}

	// 5) 覆盖既有绑定属安全敏感动作，必须落审计（NODE_REBOOTSTRAP）。
	securityLog, err := os.ReadFile(filepath.Join(dir, "security.log"))
	if err != nil {
		t.Fatalf("read security audit log: %v", err)
	}
	if !strings.Contains(string(securityLog), "NODE_REBOOTSTRAP") {
		t.Error("re-bootstrap must be recorded as a NODE_REBOOTSTRAP security event")
	}

	// 6) 节点不存在 → ErrNodeNotFound（不签发任何材料）。
	if _, err := svc.BootstrapWithToken(ctx, "no-such-node", "any-token", ""); !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("unknown node: got %v, want ErrNodeNotFound", err)
	}
}

func TestNodeService_Update_AllowConnectionsAndMaxConnections(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()

	const nodeID = "node-update-test"
	created, err := svc.RegisterNodeFromInput(ctx, ReqCreateNodeForm{
		NodeID:   nodeID,
		IsServer: false,
		Status:   model.NodeStatusPending,
	})
	if err != nil {
		t.Fatalf("register node: %v", err)
	}
	if !created.AllowConnections {
		t.Errorf("expected default AllowConnections=true, got %v", created.AllowConnections)
	}
	if created.MaxConnections != 0 {
		t.Errorf("expected default MaxConnections=0, got %d", created.MaxConnections)
	}

	// Update allowConnections=false and maxConnections=10
	disallow := false
	maxConn := uint(10)
	updated, err := svc.Update(ctx, nodeID, UpdateNodeRequest{
		AllowConnections: &disallow,
		MaxConnections:   &maxConn,
	})
	if err != nil {
		t.Fatalf("update node: %v", err)
	}
	if updated.AllowConnections {
		t.Errorf("expected AllowConnections=false after update, got %v", updated.AllowConnections)
	}
	if updated.MaxConnections != 10 {
		t.Errorf("expected MaxConnections=10 after update, got %d", updated.MaxConnections)
	}

	// Verify persistence in DB
	reloaded, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if reloaded.AllowConnections != false || reloaded.MaxConnections != 10 {
		t.Errorf("reloaded node: allow=%v, max=%d", reloaded.AllowConnections, reloaded.MaxConnections)
	}
}
