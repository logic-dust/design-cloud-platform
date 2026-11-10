package command

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/push"
	nodeService "design-cloud-engine/internal/server/service/node"
)

// newNodeOfflineService wires the single node domain for offline CLI commands
// (issue-token / issue-cert).
//
// These commands intentionally talk to the local database: they run where no
// Server process exists (bootstrap, disaster recovery, cert rotation) and must
// not start HTTP/gRPC listeners or background loops. This is the only CLI-side
// direct-DB justification; the full 17-domain assembly is never rebuilt here.
func newNodeOfflineService(cfg *config.Config) (*nodeService.Service, *db.Repos, func(), error) {
	store, err := db.NewStore(cfg.Database, slog.Default())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open database: %w", err)
	}
	repos := db.NewRepos(store)

	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  cfg.AuditLogPath(),
		SecurityLogPath: cfg.SecurityLogPath(),
		NodeID:          cfg.Runtime.NodeID,
		Version:         config.VERSION,
	})
	if err != nil {
		_ = store.Close()
		return nil, nil, nil, fmt.Errorf("init audit service: %w", err)
	}

	svc := nodeService.NewService(repos.Node, slog.Default(), nodeService.Config{
		NodeID:  cfg.Runtime.NodeID,
		CertDir: filepath.Join(cfg.Runtime.DataDir, "certs", "cluster"),
	}, auditSvc, push.NoopPublisher{}, nil, nil, nil, nil)

	cleanup := func() {
		_ = auditSvc.Close()
		_ = store.Close()
	}
	return svc, repos, cleanup, nil
}
