package command

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"design-cloud-engine/cmd/engine/console"
	"design-cloud-engine/internal/config"
	fsutil "design-cloud-engine/internal/infra/fs"
	nodeService "design-cloud-engine/internal/server/service/node"
)

func runIssueCertCommand(flags *Flags, args []string) error {
	fs := newCommandFlagSet("issue-cert", os.Stderr)

	var nodesFlag, sansFlag, csrFile, outDir, archive, configFile string
	var validDays int
	var isServer, force bool

	fs.StringVar(&configFile, "config", "", "Path to config file")
	fs.StringVar(&nodesFlag, "nodes", "", "Target Node ID(s): single ID, comma list, or topology pattern (e.g. cn01, cn01,cn02, cn[01-11])")
	fs.BoolVar(&isServer, "server", false, "Mark node as composite server role (default false: pure agent compute node)")
	fs.StringVar(&sansFlag, "hosts", "", "Comma-separated certificate SANs (default per node: <nodeID>,127.0.0.1,localhost); only with a single node")
	fs.IntVar(&validDays, "valid-days", 0, "Certificate validity in days (default: 730 for agent, 1095 for server; max 3650)")
	fs.StringVar(&csrFile, "csr", "", "Optional path to node-generated CSR (PEM). When omitted, server generates new private key; only with a single node")
	fs.StringVar(&outDir, "out-dir", "", "Export materials under this directory (mutually exclusive with --archive)")
	fs.StringVar(&archive, "archive", "", "Package materials into a tar.gz bundle (mutually exclusive with --out-dir)")
	fs.BoolVar(&force, "force", false, "Force overwrite and rotate fingerprint if node already has an active certificate")

	fs.Usage = func() {
		console.PrintStderr("Usage: %s node issue-cert --nodes <list> [options]\n\n", config.AppName)
		console.PrintStderr("Offline issue cluster certificate materials using the local CA.\n")
		console.PrintStderr("Must run on a server node with access to the database and cluster CA key.\n")
		console.PrintStderr("--nodes accepts a single ID, comma-separated IDs, or topology patterns (e.g. cn01, cn01,cn02, cn[01-02]).\n")
		console.PrintStderr("--hosts is certificate SANs for a single node, not the node list.\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("issue-cert parse error: %w", err)
	}

	nodeIDs, err := resolveTargetNodeIDs(nodesFlag)
	if err != nil {
		return err
	}
	if outDir != "" && archive != "" {
		return fmt.Errorf("--out-dir and --archive are mutually exclusive")
	}
	if len(nodeIDs) > 1 {
		if csrFile != "" {
			return fmt.Errorf("--csr is only supported with a single node")
		}
		if sansFlag != "" {
			return fmt.Errorf("--hosts (certificate SANs) is only supported with a single node; omit it so each node gets default SANs")
		}
	}

	var csrPEM []byte
	if csrFile != "" {
		data, err := os.ReadFile(csrFile)
		if err != nil {
			return fmt.Errorf("read --csr file %q: %w", csrFile, err)
		}
		csrPEM = data
	}

	configPath := cmp.Or(configFile, flags.ConfigFile, config.DefaultConfigFile)
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	nodeSvc, _, cleanup, err := newNodeOfflineService(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	certDir := filepath.Join(cfg.Runtime.StateRoot, "certs", "cluster")
	ctx := context.Background()
	sans := parseCertHosts(sansFlag)

	type issued struct {
		nodeID string
		result *nodeService.OfflineIssueCertResult
	}
	issuedList := make([]issued, 0, len(nodeIDs))

	for _, nodeID := range nodeIDs {
		req := nodeService.OfflineIssueCertRequest{
			NodeID:    nodeID,
			IsServer:  isServer,
			Hosts:     sans,
			ValidDays: validDays,
			CSRPEM:    csrPEM,
			Force:     force,
		}
		result, err := nodeSvc.OfflineIssueCert(ctx, req)
		if err != nil {
			return fmt.Errorf("offline issue certificate for %q: %w", nodeID, err)
		}
		issuedList = append(issuedList, issued{nodeID: nodeID, result: result})
	}

	var destinationDesc string
	switch {
	case archive != "":
		entries := []fsutil.ArchiveEntry{
			{Name: "ca.crt", Mode: 0644, Data: issuedList[0].result.CAPEM},
		}
		for _, item := range issuedList {
			n := item.nodeID
			entries = append(entries, fsutil.ArchiveEntry{
				Name: fmt.Sprintf("%s/%s.crt", n, n),
				Mode: 0644,
				Data: item.result.CertPEM,
			})
			if len(item.result.KeyPEM) > 0 {
				entries = append(entries, fsutil.ArchiveEntry{
					Name: fmt.Sprintf("%s/%s.key", n, n),
					Mode: 0600,
					Data: item.result.KeyPEM,
				})
			}
		}
		if err := fsutil.CreateTarGz(archive, entries); err != nil {
			return fmt.Errorf("create archive %q: %w", archive, err)
		}
		destinationDesc = fmt.Sprintf("Archive bundle: %s", archive)

	case outDir != "":
		if err := fsutil.MkdirShared(outDir); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
		caPath := filepath.Join(outDir, "ca.crt")
		if err := fsutil.WriteSharedFile(caPath, issuedList[0].result.CAPEM); err != nil {
			return fmt.Errorf("write CA file: %w", err)
		}
		for _, item := range issuedList {
			n := item.nodeID
			certPath := filepath.Join(outDir, n, fmt.Sprintf("%s.crt", n))
			keyPath := filepath.Join(outDir, n, fmt.Sprintf("%s.key", n))
			if err := fsutil.MkdirPrivate(filepath.Dir(certPath)); err != nil {
				return fmt.Errorf("create node cert directory for %q: %w", n, err)
			}
			if err := fsutil.WriteSharedFile(certPath, item.result.CertPEM); err != nil {
				return fmt.Errorf("write certificate for %q: %w", n, err)
			}
			if len(item.result.KeyPEM) > 0 {
				if err := fsutil.WritePrivateFile(keyPath, item.result.KeyPEM); err != nil {
					return fmt.Errorf("write private key for %q: %w", n, err)
				}
			}
		}
		destinationDesc = fmt.Sprintf("Directory layout under %s", outDir)

	default:
		caPath := cfg.ClusterCACertPath()
		if err := fsutil.MkdirShared(filepath.Dir(caPath)); err != nil {
			return fmt.Errorf("create cluster cert dir: %w", err)
		}
		if err := fsutil.WriteSharedFile(caPath, issuedList[0].result.CAPEM); err != nil {
			return fmt.Errorf("write cluster CA file: %w", err)
		}
		for _, item := range issuedList {
			n := item.nodeID
			nodeCertPath := cfg.ClusterNodeCertPath(n)
			nodeKeyPath := cfg.ClusterNodeKeyPath(n)
			if err := fsutil.MkdirPrivate(filepath.Dir(nodeCertPath)); err != nil {
				return fmt.Errorf("create node cert directory for %q: %w", n, err)
			}
			if err := fsutil.WriteSharedFile(nodeCertPath, item.result.CertPEM); err != nil {
				return fmt.Errorf("write node cert for %q: %w", n, err)
			}
			if len(item.result.KeyPEM) > 0 {
				if err := fsutil.WritePrivateFile(nodeKeyPath, item.result.KeyPEM); err != nil {
					return fmt.Errorf("write node key for %q: %w", n, err)
				}
			}
		}
		destinationDesc = fmt.Sprintf("Standard cluster cert layout (%s)", certDir)
	}

	role := "agent (compute node)"
	if isServer {
		role = "server (composite node)"
	}

	console.PrintlnStdout("Successfully issued offline certificate(s):")
	console.PrintStdout("   Nodes       : %d (%s)\n", len(issuedList), strings.Join(nodeIDs, ", "))
	console.PrintStdout("   Role        : %s\n", role)
	console.PrintStdout("   Destination : %s\n", destinationDesc)
	for _, item := range issuedList {
		console.PrintStdout("   - %s  fingerprint=%s  expires=%s",
			item.nodeID, item.result.Fingerprint, item.result.ExpiresAt.UTC().Format(time.RFC3339))
		if item.result.IsNewNode {
			console.PrintStdout("  [new node record]\n")
		} else {
			console.PrintStdout("  [updated binding]\n")
		}
	}

	return nil
}

func parseCertHosts(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	var result []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			result = append(result, s)
		}
	}
	return result
}
