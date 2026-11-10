package server

import (
	"log/slog"
	"testing"

	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/controlplane"
)

// stubLeader 满足装配期对 leader.Reader 的依赖；装配测试不做选举。
type stubLeader struct{}

func (stubLeader) IsLeader() bool      { return true }
func (stubLeader) LeaderID() string    { return "test-node" }
func (stubLeader) LocalEpoch() uint64  { return 1 }
func (stubLeader) LeaderEpoch() uint64 { return 1 }

// TestNewAssembly_Completeness locks the assembly graph: infrastructure,
// business services and application services are all constructed.
func TestNewAssembly_Completeness(t *testing.T) {
	_, repos := newTestSQLiteStore(t)
	auditSvc := newTestAuditService(t, "test-node")

	cfg := &config.Config{
		Runtime: config.RuntimeState{
			NodeID:  "test-node",
			DataDir: t.TempDir(),
		},
		User: config.User{
			AuthMode: config.UserAuthModeLocal,
		},
	}

	rt, err := controlplane.NewRuntime(cfg.Cluster, controlplane.Certs{}, "test-node", stubLeader{}, slog.Default(), repos)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	t.Cleanup(rt.Stop)

	assembled, err := newAssembly(rt, cfg, repos, slog.Default(), auditSvc, nil)
	if err != nil {
		t.Fatalf("newAssembly: %v", err)
	}

	if assembled.Infra == nil || assembled.Infra.Settings == nil || assembled.Infra.Push == nil ||
		assembled.Infra.Invalidator == nil || assembled.Infra.BanCache == nil {
		t.Fatalf("infra assembly incomplete: %+v", assembled.Infra)
	}
	if assembled.Application == nil || assembled.Application.Identity == nil ||
		assembled.Application.Overview == nil || assembled.Application.System == nil {
		t.Fatalf("application assembly incomplete: %+v", assembled.Application)
	}

	d := assembled.Domain
	for name, svc := range map[string]any{
		"user":       d.User,
		"role":       d.Role,
		"file":       d.File,
		"scheduler":  d.Scheduler,
		"node":       d.Node,
		"proxy":      d.Proxy,
		"session":    d.Session,
		"security":   d.Security,
		"app":        d.App,
		"license":    d.License,
		"connection": d.Connection,
		"storage":    d.Storage,
		"ticket":     d.Ticket,
		"desktop":    d.Desktop,
		"alert":      d.Alert,
	} {
		if svc == nil {
			t.Errorf("domain service %q is nil after assembly", name)
		}
	}

	// User and Session are both constructed; their shared Evictor removes the former
	// post-construction cycle while preserving the service operations.
	if d.Session == nil || d.User == nil {
		t.Fatal("user/session cycle backfill prerequisites missing")
	}
}
