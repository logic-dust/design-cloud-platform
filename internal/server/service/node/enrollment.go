package node

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/common/crypto"
	hostpkg "design-cloud-engine/internal/common/host"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func (s *Service) RegisterNode(ctx context.Context, node *model.Node) error {
	if node == nil {
		return fmt.Errorf("%w: node is required", ErrInvalidNodeID)
	}

	nodeID, err := validateNodeID(node.NodeID)
	if err != nil {
		return err
	}
	node.NodeID = nodeID

	if node.Status == 0 {
		node.Status = model.NodeStatusPending
	}
	clientHost, err := normalizeClientHost(node.ClientHost)
	if err != nil {
		return err
	}
	node.ClientHost = clientHost
	if node.CertFingerprint != "" {
		fp, err := normalizeFingerprint(node.CertFingerprint)
		if err != nil {
			return err
		}
		node.CertFingerprint = fp
	}

	// Auto-generate token if not provided
	if node.Token == "" {
		node.Token = crypto.GenSecret(24)
		node.TokenCreatedAt = time.Now()
	}

	if err := s.nodeRepo.Create(ctx, node); err != nil {
		if common.IsUniqueConstraintError(err) {
			return fmt.Errorf("%w: %s", ErrNodeAlreadyRegistered, nodeID)
		}
		return fmt.Errorf("create node %q: %w", nodeID, err)
	}

	s.auditSvc.LogNode(ctx, "NODE_REGISTER", "success", node.NodeID, slog.LevelInfo, map[string]any{
		"is_server": node.IsServer,
	})

	return nil
}

// RegisterNodeFromInput registers a node from writable input fields only.
func (s *Service) RegisterNodeFromInput(ctx context.Context, form ReqCreateNodeForm) (model.Node, error) {
	node := buildNodeFromCreateForm(form)
	return node, s.RegisterNode(ctx, &node)
}

func (s *Service) GenerateToken(ctx context.Context, nodeID string) (*model.Node, error) {
	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q for token generation: %w", nodeID, err)
	}
	node.Token = crypto.GenSecret(24)
	node.TokenCreatedAt = time.Now()
	if err := s.nodeRepo.UpdateAllFields(ctx, &node); err != nil {
		return nil, fmt.Errorf("generate token for node %q: %w", nodeID, err)
	}

	s.auditSvc.LogNode(ctx, "NODE_TOKEN_GENERATE", "success", nodeID, slog.LevelInfo, nil)

	return &node, nil
}

func (s *Service) UpdateCert(ctx context.Context, nodeID string, fingerprint string, subject string, expiresAt time.Time) (*model.Node, error) {
	var err error
	nodeID, err = validateNodeID(nodeID)
	if err != nil {
		return nil, err
	}

	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q for cert update: %w", nodeID, err)
	}

	if fingerprint != "" {
		fp, err := normalizeFingerprint(fingerprint)
		if err != nil {
			return nil, err
		}
		// Rotate the previous fingerprint (dual fingerprint): the old one stays
		// valid through the renewal switch window and for .prev rollback.
		if node.CertFingerprint != "" && node.CertFingerprint != fp {
			node.CertFingerprintPrev = node.CertFingerprint
		}
		node.CertFingerprint = fp
		node.CertSubject = subject
		if !expiresAt.IsZero() {
			node.CertExpiresAt = expiresAt
		}
		// A (re)issued certificate resets the expiry alert stage.
		node.CertAlertStage = 0
	} else {
		node.CertFingerprint = ""
		node.CertFingerprintPrev = ""
		node.CertSubject = ""
		node.CertExpiresAt = time.Time{}
		node.CertAlertStage = 0
	}

	if err := s.nodeRepo.UpdateAllFields(ctx, &node); err != nil {
		return nil, fmt.Errorf("update cert for node %q: %w", nodeID, err)
	}

	s.auditSvc.LogNode(ctx, "NODE_CERT_UPDATE", "success", nodeID, slog.LevelInfo, map[string]any{
		"has_fingerprint": fingerprint != "",
	})

	return &node, nil
}

// BootstrapWithToken validates the one-time token, signs a node certificate,
// persists the binding, and returns PEM materials.
func (s *Service) BootstrapWithToken(ctx context.Context, nodeID, token, serverAddr string) (*contract.BootstrapMaterials, error) {
	node, err := s.resolveBootstrapNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if err := s.verifyBootstrapToken(ctx, node, token); err != nil {
		return nil, err
	}

	// A node that already holds a certificate is being re-provisioned: the newly
	// issued certificate replaces the stored fingerprint binding, so the previous
	// certificate stops being accepted. Surfaced as a security event below.
	previousFingerprint := node.CertFingerprint

	result, err := IssueNodeCert(CertOptions{
		CommonName: node.NodeID,
		Hosts:      []string{node.NodeID, "127.0.0.1", "localhost"},
		ValidDays:  AgentCertValidDays,
		CACertFile: s.clusterCACertPath(),
		CAKeyFile:  s.clusterCAKeyPath(),
		CertFile:   s.clusterNodeCertPath(node.NodeID),
		KeyFile:    s.clusterNodeKeyPath(node.NodeID),
	})
	if err != nil {
		s.auditSvc.LogSecurity(ctx, "NODE_BOOTSTRAP", "failed", slog.LevelError, map[string]any{
			"node_id": node.NodeID,
			"reason":  "certificate_issue_failed",
			"error":   err.Error(),
		})
		return nil, err
	}

	if err := s.persistCertBinding(ctx, &node, result); err != nil {
		return nil, err
	}

	materials, err := s.loadBootstrapMaterials(node.NodeID, serverAddr, result)
	if err != nil {
		return nil, err
	}

	// A valid OTP authorizes issuing OR re-issuing a node certificate, so
	// re-bootstrapping an already-provisioned node overwrites its identity binding.
	// Record that at WARN with the superseded fingerprint to keep token-driven
	// identity rotation auditable.
	if previousFingerprint != "" {
		s.auditSvc.LogSecurity(ctx, "NODE_REBOOTSTRAP", "success", slog.LevelWarn, map[string]any{
			"node_id":              node.NodeID,
			"previous_fingerprint": fingerprintPrefix(previousFingerprint),
			"cert_fingerprint":     node.CertFingerprint,
		})
	}

	s.auditSvc.LogSecurity(ctx, "NODE_BOOTSTRAP", "success", slog.LevelInfo, map[string]any{
		"node_id":          node.NodeID,
		"cert_fingerprint": node.CertFingerprint,
		"cert_subject":     node.CertSubject,
		"cert_expires_at":  node.CertExpiresAt,
		"rebootstrap":      previousFingerprint != "",
	})

	return materials, nil
}

// resolveBootstrapNode validates the node ID and loads the node record.
// A valid OTP is the authorization to issue or re-issue the node certificate.
// Already-provisioned nodes must be allowed through: token rotation
// (issue-token on a node that already has a fingerprint) is the supported
// re-bootstrap path, and rejecting it blocks in-place reinstall.
func (s *Service) resolveBootstrapNode(ctx context.Context, nodeID string) (model.Node, error) {
	nodeID, err := validateNodeID(nodeID)
	if err != nil {
		s.auditSvc.LogSecurity(ctx, "NODE_BOOTSTRAP", "failed", slog.LevelWarn, map[string]any{
			"reason": "missing_node_id",
		})
		return model.Node{}, err
	}

	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		s.auditSvc.LogSecurity(ctx, "NODE_BOOTSTRAP", "failed", slog.LevelWarn, map[string]any{
			"node_id": nodeID,
			"reason":  "node_not_found",
		})
		if errors.Is(err, common.ErrNotFound) {
			return model.Node{}, ErrNodeNotFound
		}
		return model.Node{}, fmt.Errorf("get node %q for bootstrap: %w", nodeID, err)
	}
	return node, nil
}

// verifyBootstrapToken checks the token exists, matches the supplied token in
// constant time, and has not expired.
func (s *Service) verifyBootstrapToken(ctx context.Context, node model.Node, token string) error {
	if node.Token == "" {
		s.auditSvc.LogSecurity(ctx, "NODE_BOOTSTRAP", "failed", slog.LevelWarn, map[string]any{
			"node_id": node.NodeID,
			"reason":  "missing_token",
		})
		return fmt.Errorf("%w: %s", ErrMissingNodeToken, node.NodeID)
	}
	if subtle.ConstantTimeCompare([]byte(node.Token), []byte(token)) != 1 {
		s.auditSvc.LogSecurity(ctx, "NODE_BOOTSTRAP", "failed", slog.LevelWarn, map[string]any{
			"node_id": node.NodeID,
			"reason":  "invalid_token",
		})
		return fmt.Errorf("%w: %s", ErrInvalidNodeToken, node.NodeID)
	}
	if !node.TokenCreatedAt.IsZero() && time.Since(node.TokenCreatedAt) > TokenExpiry {
		s.auditSvc.LogSecurity(ctx, "NODE_BOOTSTRAP", "failed", slog.LevelWarn, map[string]any{
			"node_id":          node.NodeID,
			"token_created_at": node.TokenCreatedAt,
			"reason":           "token_expired",
		})
		return fmt.Errorf("%w: %s (generated at %v)", ErrExpiredNodeToken, node.NodeID, node.TokenCreatedAt)
	}
	return nil
}

// persistCertBinding writes the issued certificate metadata to the node record
// and clears the one-time bootstrap token.
func (s *Service) persistCertBinding(ctx context.Context, node *model.Node, result *CertResult) error {
	expiresAt, err := time.Parse(time.RFC3339, result.ExpiresAt)
	if err != nil {
		return fmt.Errorf("parse certificate expiry: %w", err)
	}

	node.CertFingerprint = result.Fingerprint
	node.CertSubject = result.Subject
	node.CertExpiresAt = expiresAt
	node.Token = ""
	node.Status = model.NodeStatusProvisioned
	if err := s.nodeRepo.UpdateAllFields(ctx, node); err != nil {
		s.auditSvc.LogSecurity(ctx, "NODE_BOOTSTRAP", "failed", slog.LevelError, map[string]any{
			"node_id": node.NodeID,
			"reason":  "db_save_failed",
			"error":   err.Error(),
		})
		return fmt.Errorf("bootstrap node %q: persist cert binding: %w", node.NodeID, err)
	}
	return nil
}

// loadBootstrapMaterials reads the issued PEM files from disk and assembles the
// response payload for the agent.
func (s *Service) loadBootstrapMaterials(nodeID, serverAddr string, result *CertResult) (*contract.BootstrapMaterials, error) {
	certPEM, err := os.ReadFile(result.CertPath)
	if err != nil {
		return nil, fmt.Errorf("read issued certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(result.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("read issued key: %w", err)
	}
	caPEM, err := os.ReadFile(s.clusterCACertPath())
	if err != nil {
		return nil, fmt.Errorf("read cluster CA: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, result.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("parse certificate expiry: %w", err)
	}

	return &contract.BootstrapMaterials{
		NodeID:        nodeID,
		ServerAddr:    serverAddr,
		CertPEM:       string(certPEM),
		KeyPEM:        string(keyPEM),
		CAPEM:         string(caPEM),
		CertExpiresAt: expiresAt,
	}, nil
}

func (s *Service) CreateClusterCert(ctx context.Context, req CreateClusterCertRequest) (*CertResult, error) {
	nodeID, err := validateNodeID(req.NodeID)
	if err != nil {
		return nil, err
	}

	hosts := req.Hosts
	if len(hosts) == 0 {
		hosts = []string{nodeID, "127.0.0.1", "localhost"}
	}

	result, err := IssueNodeCert(CertOptions{
		CommonName: nodeID,
		Hosts:      hosts,
		ValidDays:  req.ValidDays,
		CACertFile: s.clusterCACertPath(),
		CAKeyFile:  s.clusterCAKeyPath(),
		CertFile:   s.clusterNodeCertPath(nodeID),
		KeyFile:    s.clusterNodeKeyPath(nodeID),
	})
	if err != nil {
		return nil, err
	}

	if req.SyncNode {
		expiresAt, err := time.Parse(time.RFC3339, result.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("parse certificate expiry: %w", err)
		}
		if _, err := s.UpdateCert(ctx, result.CommonName, result.Fingerprint, result.Subject, expiresAt); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func normalizeFingerprint(input string) (string, error) {
	clean := strings.ToLower(strings.ReplaceAll(input, ":", ""))
	if clean == "" {
		return "", fmt.Errorf("%w: cert_fingerprint is required", ErrInvalidNodeCert)
	}
	if len(clean) != 64 {
		return "", fmt.Errorf("%w: cert_fingerprint must be 64 hex chars", ErrInvalidNodeCert)
	}
	for _, r := range clean {
		if !unicode.IsDigit(r) && (r < 'a' || r > 'f') {
			return "", fmt.Errorf("%w: cert_fingerprint contains invalid hex chars", ErrInvalidNodeCert)
		}
	}
	return clean, nil
}

// fingerprintPrefix returns a short, log-safe prefix of a certificate fingerprint so
// audit records can correlate a rotation without persisting the whole value.
func fingerprintPrefix(fp string) string {
	const width = 16
	if len(fp) <= width {
		return fp
	}
	return fp[:width] + "..."
}

func buildNodeFromCreateForm(form ReqCreateNodeForm) model.Node {
	node := model.Node{
		NodeID:           form.NodeID,
		IsServer:         form.IsServer,
		ClientHost:       form.ClientHost,
		AllowConnections: true,
		MaxConnections:   form.MaxConnections,
	}
	if form.AllowConnections != nil {
		node.AllowConnections = *form.AllowConnections
	}
	if form.Status != 0 {
		node.Status = form.Status
	}
	if len(form.Labels) > 0 {
		node.Labels = form.Labels
	}
	return node
}

func normalizeClientHost(input string) (string, error) {
	host := input
	if host == "" {
		return "", nil
	}
	if err := hostpkg.ValidateHost(host); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidClientHost, err)
	}
	return host, nil
}
