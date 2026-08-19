package command

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/common"
	fsutil "design-cloud-engine/internal/common/fs"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	nodeService "design-cloud-engine/internal/server/service/node"
	"design-cloud-engine/internal/server/service/push"
)

func runIssueCertCommand(flags *Flags, args []string) error {
	fs := newCommandFlagSet("issue-cert", os.Stderr)

	var hostname, hostsFlag, csrFile, outDir, archive, configFile string
	var validDays int
	var isServer, force bool

	fs.StringVar(&configFile, "config", "", "Path to config file")
	fs.StringVar(&hostname, "hostname", "", "Target Node ID to issue certificate for (required)")
	fs.BoolVar(&isServer, "server", false, "Mark node as composite server role (default false: pure agent compute node)")
	fs.StringVar(&hostsFlag, "hosts", "", "Comma-separated SANs (default: <nodeID>,127.0.0.1,localhost)")
	fs.IntVar(&validDays, "valid-days", 0, "Certificate validity in days (default: 730 for agent, 1095 for server; max 3650)")
	fs.StringVar(&csrFile, "csr", "", "Optional path to node-generated CSR (PEM). When omitted, server generates new private key")
	fs.StringVar(&outDir, "out-dir", "", "Export materials under this directory (mutually exclusive with --archive)")
	fs.StringVar(&archive, "archive", "", "Package materials into a tar.gz bundle (mutually exclusive with --out-dir)")
	fs.BoolVar(&force, "force", false, "Force overwrite and rotate fingerprint if node already has an active certificate")

	fs.Usage = func() {
		common.PrintStderr("Usage: %s node issue-cert --hostname <nodeID> [options]\n\n", config.AppName)
		common.PrintStderr("Offline issue cluster certificate materials for a node directly using the local CA.\n")
		common.PrintStderr("Must run on a server node with access to the database and cluster CA key.\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("issue-cert parse error: %w", err)
	}

	if hostname == "" {
		return fmt.Errorf("--hostname is required")
	}
	if outDir != "" && archive != "" {
		return fmt.Errorf("--out-dir and --archive are mutually exclusive")
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

	certDir := filepath.Join(cfg.Runtime.DataDir, "certs", "cluster")
	nodeSvc := nodeService.NewService(repos.Node, slog.Default(), nodeService.Config{
		NodeID:  cfg.Runtime.NodeID,
		CertDir: certDir,
	}, auditSvc, push.NoopPublisher{}, nil, nil)

	ctx := context.Background()
	req := nodeService.OfflineIssueCertRequest{
		NodeID:    hostname,
		IsServer:  isServer,
		Hosts:     parseCertHosts(hostsFlag),
		ValidDays: validDays,
		CSRPEM:    csrPEM,
		Force:     force,
	}

	result, err := nodeSvc.OfflineIssueCert(ctx, req)
	if err != nil {
		return fmt.Errorf("offline issue certificate: %w", err)
	}

	var destinationDesc string
	switch {
	case archive != "":
		entries := []fsutil.ArchiveEntry{
			{Name: "ca.crt", Mode: 0644, Data: result.CAPEM},
			{Name: fmt.Sprintf("%s/%s.crt", hostname, hostname), Mode: 0644, Data: result.CertPEM},
		}
		if len(result.KeyPEM) > 0 {
			entries = append(entries, fsutil.ArchiveEntry{
				Name: fmt.Sprintf("%s/%s.key", hostname, hostname),
				Mode: 0600,
				Data: result.KeyPEM,
			})
		}
		if err := fsutil.CreateTarGz(archive, entries); err != nil {
			return fmt.Errorf("create archive %q: %w", archive, err)
		}
		destinationDesc = fmt.Sprintf("Archive bundle: %s", archive)

	case outDir != "":
		caPath := filepath.Join(outDir, "ca.crt")
		certPath := filepath.Join(outDir, hostname, fmt.Sprintf("%s.crt", hostname))
		keyPath := filepath.Join(outDir, hostname, fmt.Sprintf("%s.key", hostname))

		if err := fsutil.MkdirShared(outDir); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
		if err := fsutil.WriteSharedFile(caPath, result.CAPEM); err != nil {
			return fmt.Errorf("write CA file: %w", err)
		}
		if err := fsutil.MkdirPrivate(filepath.Dir(certPath)); err != nil {
			return fmt.Errorf("create node cert directory: %w", err)
		}
		if err := fsutil.WriteSharedFile(certPath, result.CertPEM); err != nil {
			return fmt.Errorf("write certificate file: %w", err)
		}
		if len(result.KeyPEM) > 0 {
			if err := fsutil.WritePrivateFile(keyPath, result.KeyPEM); err != nil {
				return fmt.Errorf("write private key file: %w", err)
			}
		}
		destinationDesc = fmt.Sprintf("Directory layout under %s", outDir)

	default:
		// Default: write straight into standard cluster certificate directory
		nodeCertPath := cfg.ClusterNodeCertPath(hostname)
		nodeKeyPath := cfg.ClusterNodeKeyPath(hostname)
		caPath := cfg.ClusterCACertPath()

		if err := fsutil.MkdirShared(filepath.Dir(caPath)); err != nil {
			return fmt.Errorf("create cluster cert dir: %w", err)
		}
		if err := fsutil.WriteSharedFile(caPath, result.CAPEM); err != nil {
			return fmt.Errorf("write cluster CA file: %w", err)
		}
		if err := fsutil.MkdirPrivate(filepath.Dir(nodeCertPath)); err != nil {
			return fmt.Errorf("create node cert directory: %w", err)
		}
		if err := fsutil.WriteSharedFile(nodeCertPath, result.CertPEM); err != nil {
			return fmt.Errorf("write node cert file: %w", err)
		}
		if len(result.KeyPEM) > 0 {
			if err := fsutil.WritePrivateFile(nodeKeyPath, result.KeyPEM); err != nil {
				return fmt.Errorf("write node key file: %w", err)
			}
		}
		destinationDesc = fmt.Sprintf("Standard cluster cert layout (%s)", certDir)
	}

	role := "agent (compute node)"
	if isServer {
		role = "server (composite node)"
	}

	common.PrintlnStdout("Successfully issued offline certificate:")
	common.PrintStdout("   Node ID     : %s\n", hostname)
	common.PrintStdout("   Role        : %s\n", role)
	common.PrintStdout("   Fingerprint : %s\n", result.Fingerprint)
	common.PrintStdout("   Subject     : %s\n", result.Subject)
	common.PrintStdout("   Expires At  : %s\n", result.ExpiresAt.UTC().Format(time.RFC3339))
	common.PrintStdout("   Destination : %s\n", destinationDesc)
	if result.IsNewNode {
		common.PrintStdout("   DB Status   : Created new node record (status: provisioned)\n")
	} else {
		common.PrintStdout("   DB Status   : Updated certificate binding on existing node\n")
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
