package command

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	nodeService "design-cloud-engine/internal/server/service/node"
	"design-cloud-engine/internal/server/service/push"
)

func runIssueTokenCommand(flags *Flags, args []string) error {
	fs := newCommandFlagSet("issue-token", os.Stderr)
	var hostname, configFile string
	var isServer bool
	fs.StringVar(&configFile, "config", "", "Path to config file")
	fs.StringVar(&hostname, "hostname", "", "Node ID to issue token for")
	fs.BoolVar(&isServer, "server", false, "Mark as server role (default false: pure agent compute node)")
	fs.Usage = func() {
		common.PrintStderr("Usage: %s node issue-token --hostname <nodeID> [--server] [--config <path>]\n\n", config.AppName)
		common.PrintStderr("Issue or rotate a bootstrap token for a cluster node.\n")
		common.PrintStderr("If the node does not exist, it will be created (defaults to agent; use --server for composite server node).\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("issue-token parse error: %w", err)
	}

	if hostname == "" {
		return fmt.Errorf("--hostname is required")
	}

	configPath := cmp.Or(configFile, flags.ConfigFile, config.DefaultConfigFile)
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	store, err := db.NewStore(cfg.Database, slog.Default())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = store.Close() }()

	repos := db.NewRepos(store)

	auditSvc, err := audit.NewService(audit.Config{
		PrimaryLogPath:  cfg.AuditLogPath(),
		SecurityLogPath: cfg.SecurityLogPath(),
		NodeID:          cfg.Runtime.NodeID,
		Version:         config.VERSION,
	})
	if err != nil {
		return fmt.Errorf("init audit service: %w", err)
	}
	defer func() { _ = auditSvc.Close() }()
	nodeSvc := nodeService.NewService(repos.Node, slog.Default(), nodeService.Config{
		NodeID:  cfg.Runtime.NodeID,
		CertDir: filepath.Join(cfg.Runtime.DataDir, "certs", "cluster"),
	}, auditSvc, push.NoopPublisher{}, nil, nil)
	ctx := context.Background()

	existingNode, err := repos.Node.GetByNodeID(ctx, hostname)
	if err != nil {
		if !errors.Is(err, common.ErrNotFound) {
			return fmt.Errorf("lookup node %q: %w", hostname, err)
		}
		form := nodeService.ReqCreateNodeForm{
			NodeID:   hostname,
			IsServer: isServer,
			Status:   model.NodeStatusPending,
		}
		createdNode, err := nodeSvc.RegisterNodeFromInput(ctx, form)
		if err != nil {
			return fmt.Errorf("register node: %w", err)
		}
		displayToken(hostname, createdNode.Token, createdNode.TokenCreatedAt)
	} else {
		if existingNode.CertFingerprint != "" {
			common.PrintStdout("Node '%s' is already bootstrapped (has certificate). A new token will overwrite the old one.\n", hostname)
		}
		updatedNode, err := nodeSvc.GenerateToken(ctx, hostname)
		if err != nil {
			return fmt.Errorf("issue token: %w", err)
		}
		displayToken(hostname, updatedNode.Token, updatedNode.TokenCreatedAt)
	}

	return nil
}

func displayToken(nodeID, token string, createdAt time.Time) {
	common.PrintlnStdout("")
	common.PrintStdout("  Node ID     : %s\n", nodeID)
	common.PrintStdout("  Token       : %s\n", token)
	common.PrintStdout("  Created At  : %s\n", createdAt.UTC().Format(time.RFC3339))
	common.PrintStdout("  Expires In  : %s (at %s)\n", nodeService.TokenExpiry, createdAt.Add(nodeService.TokenExpiry).UTC().Format(time.RFC3339))
	common.PrintlnStdout("")
	common.PrintStdout("  Run the following command to retrieve node certificates:\n")
	common.PrintStdout("    engine node register --token %s --portal-url <portal-url>\n", token)
	common.PrintlnStdout("")
	common.PrintStdout("  * Note: In a non-shared storage environment, you MUST run this command on the actual target node.\n")
	common.PrintStdout("    In a shared NFS environment, you can run it on any node with access to the shared certs/ directory.\n")
	common.PrintlnStdout("")
}
