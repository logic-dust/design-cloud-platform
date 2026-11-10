package command

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"design-cloud-engine/cmd/engine/console"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/platform/config"
	nodeService "design-cloud-engine/internal/server/service/node"
)

func runIssueTokenCommand(flags *Flags, args []string) error {
	fs := newCommandFlagSet("issue-token", os.Stderr)
	var hostname, configFile string
	var isServer bool
	fs.StringVar(&configFile, "config", "", "Path to config file")
	fs.StringVar(&hostname, "hostname", "", "Node ID to issue token for")
	fs.BoolVar(&isServer, "server", false, "Mark as server role (default false: pure agent compute node)")
	fs.Usage = func() {
		console.PrintStderr("Usage: %s node issue-token --hostname <nodeID> [--server] [--config <path>]\n\n", config.AppName)
		console.PrintStderr("Issue or rotate an enrollment token for a cluster node.\n")
		console.PrintStderr("If the node does not exist, it will be created (defaults to agent; use --server for composite server node).\n\n")
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

	nodeSvc, repos, cleanup, err := newNodeOfflineService(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := context.Background()

	existingNode, err := repos.Node.GetByNodeID(ctx, hostname)
	if err != nil {
		if !errors.Is(err, contract.ErrNotFound) {
			return fmt.Errorf("lookup node %q: %w", hostname, err)
		}
		form := nodeService.ReqCreateNodeForm{
			NodeID:   hostname,
			IsServer: isServer,
		}
		_, issued, err := nodeSvc.RegisterNodeFromInput(ctx, form)
		if err != nil {
			return fmt.Errorf("register node: %w", err)
		}
		if issued == nil {
			return fmt.Errorf("register node: no enrollment token issued")
		}
		displayToken(issued)
	} else {
		if existingNode.CertFingerprint != "" {
			console.PrintStdout("Node '%s' is already enrolled (has certificate). A new token will overwrite the old one.\n", hostname)
		}
		updatedToken, err := nodeSvc.GenerateToken(ctx, hostname)
		if err != nil {
			return fmt.Errorf("issue token: %w", err)
		}
		displayToken(updatedToken)
	}

	return nil
}

func displayToken(issued *nodeService.EnrollmentTokenResponse) {
	console.PrintlnStdout("")
	console.PrintStdout("  Node ID     : %s\n", issued.NodeID)
	console.PrintStdout("  Token       : %s\n", issued.Token)
	console.PrintStdout("  Expires At  : %s\n", issued.ExpiresAt.UTC().Format(time.RFC3339))
	console.PrintStdout("  Expires In  : %s\n", nodeService.TokenExpiry)
	console.PrintlnStdout("")
	console.PrintStdout("  Run the following command to retrieve node certificates:\n")
	console.PrintStdout("    engine node register --token %s --portal-url <portal-url>\n", issued.Token)
	console.PrintlnStdout("")
	console.PrintStdout("  * Note: In a non-shared storage environment, you MUST run this command on the actual target node.\n")
	console.PrintStdout("    In a shared NFS environment, you can run it on any node with access to the shared certs/ directory.\n")
	console.PrintlnStdout("")
}
