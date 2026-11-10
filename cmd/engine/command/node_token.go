package command

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"design-cloud-engine/cmd/engine/console"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	nodeService "design-cloud-engine/internal/server/service/node"
)

func runIssueTokenCommand(flags *Flags, args []string) error {
	fs := newCommandFlagSet("issue-token", os.Stderr)
	var nodesFlag, configFile string
	var isServer bool
	fs.StringVar(&configFile, "config", "", "Path to config file")
	fs.StringVar(&nodesFlag, "nodes", "", "Target Node ID(s): single ID, comma list, or topology pattern (e.g. cn01, cn01,cn02, cn[01-11])")
	fs.BoolVar(&isServer, "server", false, "Mark as server role (default false: pure agent compute node)")
	fs.Usage = func() {
		console.PrintStderr("Usage: %s node issue-token --nodes <list> [--server] [--config <path>]\n\n", config.AppName)
		console.PrintStderr("Issue or rotate enrollment token(s) for one or more cluster nodes.\n")
		console.PrintStderr("If a node does not exist, it will be created (defaults to agent; use --server for composite server node).\n")
		console.PrintStderr("--nodes accepts a single ID, comma-separated IDs, or topology patterns (e.g. cn01, cn01,cn02, cn[01-02]).\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("issue-token parse error: %w", err)
	}

	nodeIDs, err := resolveTargetNodeIDs(nodesFlag)
	if err != nil {
		return err
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
	issuedList := make([]*nodeService.EnrollmentTokenResponse, 0, len(nodeIDs))

	for _, nodeID := range nodeIDs {
		existingNode, err := repos.Node.GetByNodeID(ctx, nodeID)
		if err != nil {
			if !errors.Is(err, contract.ErrNotFound) {
				return fmt.Errorf("lookup node %q: %w", nodeID, err)
			}
			form := nodeService.ReqCreateNodeForm{
				NodeID:   nodeID,
				IsServer: isServer,
			}
			_, issued, err := nodeSvc.RegisterNodeFromInput(ctx, form)
			if err != nil {
				return fmt.Errorf("register node %q: %w", nodeID, err)
			}
			if issued == nil {
				return fmt.Errorf("register node %q: no enrollment token issued", nodeID)
			}
			issuedList = append(issuedList, issued)
			continue
		}

		if existingNode.CertFingerprint != "" {
			console.PrintStdout("Node '%s' is already enrolled (has certificate). A new token will overwrite the old one.\n", nodeID)
		}
		updatedToken, err := nodeSvc.GenerateToken(ctx, nodeID)
		if err != nil {
			return fmt.Errorf("issue token for %q: %w", nodeID, err)
		}
		issuedList = append(issuedList, updatedToken)
	}

	if len(issuedList) == 1 {
		displayToken(issuedList[0])
	} else {
		displayTokenBatch(issuedList)
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
	console.PrintStdout("  Run the following command on the target node to retrieve certificates:\n")
	console.PrintStdout("    dc-engine node register --portal-url https://<portal-host>:<port> --token %s\n", issued.Token)
	displayTokenFooter()
}

func displayTokenBatch(issuedList []*nodeService.EnrollmentTokenResponse) {
	console.PrintlnStdout("")
	console.PrintStdout("Successfully issued %d enrollment token(s) (expires in %s):\n", len(issuedList), nodeService.TokenExpiry)
	for _, issued := range issuedList {
		console.PrintStdout("  - %-20s  token=%s  expires=%s\n",
			issued.NodeID, issued.Token, issued.ExpiresAt.UTC().Format(time.RFC3339))
	}
	console.PrintlnStdout("")
	console.PrintStdout("  Run the following command on each target node to retrieve certificates:\n")
	console.PrintStdout("    dc-engine node register --portal-url https://<portal-host>:<port> --token <token>\n")
	displayTokenFooter()
}

func displayTokenFooter() {
	console.PrintStdout("  If Portal uses a private/self-signed CA, also pass:\n")
	console.PrintStdout("    --ca /path/to/portal.ca.crt\n")
	console.PrintlnStdout("")
	console.PrintStdout("  * Note: In a non-shared storage environment, you MUST run this command on the actual target node.\n")
	console.PrintStdout("    In a shared NFS environment, you can run it on any node with access to the shared certs/ directory.\n")
	console.PrintlnStdout("")
}
