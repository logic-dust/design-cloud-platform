package node

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/crypto"
	hostpkg "design-cloud-engine/internal/platform/host"
)

func hashEnrollmentToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func issueEnrollmentToken(now time.Time) (plain, hash string, expires time.Time) {
	plain = crypto.GenSecret(32)
	hash = hashEnrollmentToken(plain)
	expires = now.UTC().Add(TokenExpiry)
	return plain, hash, expires
}

func (s *Service) RegisterNode(ctx context.Context, node *model.Node) (*EnrollmentTokenResponse, error) {
	if node == nil {
		return nil, fmt.Errorf("%w: node is required", ErrInvalidNodeID)
	}

	nodeID, err := validateNodeID(node.NodeID)
	if err != nil {
		return nil, err
	}
	node.NodeID = nodeID
	node.Status = model.NodeStatusUnset

	dialHost, err := normalizeDialHost(node.DialHost)
	if err != nil {
		return nil, err
	}
	node.DialHost = dialHost
	if err := validateDescription(node.Description); err != nil {
		return nil, err
	}
	node.Groups = model.NormalizeNodeGroups(node.Groups)
	for _, g := range node.Groups {
		if err := model.ValidateNodeGroupName(g); err != nil {
			return nil, contract.NewValidationError(err.Error())
		}
	}

	if node.CertFingerprint != "" {
		fp, err := normalizeFingerprint(node.CertFingerprint)
		if err != nil {
			return nil, err
		}
		node.CertFingerprint = fp
	}

	var issued *EnrollmentTokenResponse
	if node.EnrollmentTokenHash == "" {
		plain, hash, expires := issueEnrollmentToken(time.Now())
		node.EnrollmentTokenHash = hash
		node.EnrollmentTokenExpiresAt = expires
		issued = &EnrollmentTokenResponse{NodeID: nodeID, Token: plain, ExpiresAt: expires}
	}

	if err := s.nodeRepo.Create(ctx, node); err != nil {
		if db.IsUniqueConstraintError(err) {
			return nil, fmt.Errorf("%w: %s", ErrNodeAlreadyRegistered, nodeID)
		}
		return nil, fmt.Errorf("create node %q: %w", nodeID, err)
	}

	s.auditSvc.LogNode(ctx, "NODE_REGISTER", "success", node.NodeID, slog.LevelInfo, map[string]any{
		"is_server": node.IsServer,
	})

	return issued, nil
}

func (s *Service) RegisterNodeFromInput(ctx context.Context, form ReqCreateNodeForm) (NodeAdminView, *EnrollmentTokenResponse, error) {
	node := buildNodeFromCreateForm(form)
	issued, err := s.RegisterNode(ctx, &node)
	if err != nil {
		return NodeAdminView{}, nil, err
	}
	created, err := s.nodeRepo.GetByNodeID(ctx, node.NodeID)
	if err != nil {
		return NodeAdminView{}, issued, err
	}
	return BuildNodeAdminView(created), issued, nil
}

func (s *Service) GenerateToken(ctx context.Context, nodeID string) (*EnrollmentTokenResponse, error) {
	if _, err := s.nodeRepo.GetByNodeID(ctx, nodeID); err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q for token generation: %w", nodeID, err)
	}
	plain, hash, expires := issueEnrollmentToken(time.Now())
	if err := s.nodeRepo.SetEnrollmentToken(ctx, nodeID, hash, expires); err != nil {
		return nil, fmt.Errorf("generate token for node %q: %w", nodeID, err)
	}
	s.auditSvc.LogNode(ctx, "NODE_TOKEN_GENERATE", "success", nodeID, slog.LevelInfo, nil)
	return &EnrollmentTokenResponse{NodeID: nodeID, Token: plain, ExpiresAt: expires}, nil
}

func certFingerprintPatch(existingFingerprint, newFingerprint, subject string, expiresAt time.Time) db.NodeCertPatch {
	prev := ""
	if existingFingerprint != "" && existingFingerprint != newFingerprint {
		prev = existingFingerprint
	}
	zero := int16(0)
	return db.NodeCertPatch{
		CertFingerprint:     newFingerprint,
		CertFingerprintPrev: prev,
		CertSubject:         subject,
		CertExpiresAt:       expiresAt,
		CertAlertStage:      &zero,
	}
}

func (s *Service) UpdateCert(ctx context.Context, nodeID string, fingerprint string, subject string, expiresAt time.Time) (*NodeAdminView, error) {
	nodeID, err := validateNodeID(nodeID)
	if err != nil {
		return nil, err
	}

	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("get node %q for cert update: %w", nodeID, err)
	}

	patch := db.NodeCertPatch{}
	if fingerprint != "" {
		fp, err := normalizeFingerprint(fingerprint)
		if err != nil {
			return nil, err
		}
		patch = certFingerprintPatch(node.CertFingerprint, fp, subject, expiresAt)
	} else {
		patch.ClearCert = true
	}

	if err := s.nodeRepo.UpdateCertFields(ctx, nodeID, patch); err != nil {
		return nil, fmt.Errorf("update cert for node %q: %w", nodeID, err)
	}

	s.auditSvc.LogNode(ctx, "NODE_CERT_UPDATE", "success", nodeID, slog.LevelInfo, map[string]any{
		"has_fingerprint": fingerprint != "",
	})

	updated, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	view := BuildNodeAdminView(updated)
	return &view, nil
}

// Enrollment contains the issued certificate, key, CA, and server endpoint
// returned to a node during OTP enrollment.
type Enrollment struct {
	NodeID        string    `json:"nodeId"`
	ServerAddr    string    `json:"serverAddr"`
	CertPEM       string    `json:"certPem"`
	KeyPEM        string    `json:"keyPem"`
	CAPEM         string    `json:"caPem"`
	CertExpiresAt time.Time `json:"certExpiresAt"`
}

func (s *Service) EnrollWithToken(ctx context.Context, nodeID, token, serverAddr string) (*Enrollment, error) {
	nodeID, err := validateNodeID(nodeID)
	if err != nil {
		s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "failed", slog.LevelWarn, map[string]any{
			"reason": "missing_node_id",
		})
		return nil, err
	}

	lock := s.nodeCertLock(nodeID)
	lock.Lock()
	defer lock.Unlock()

	// Re-read under the per-node lock so fingerprint/token stay consistent with CreateClusterCert.
	node, err := s.resolveEnrollNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	if err := s.verifyEnrollmentToken(ctx, node, token); err != nil {
		return nil, err
	}

	previousFingerprint := node.CertFingerprint
	tokenHash := hashEnrollmentToken(token)

	consumed, err := s.nodeRepo.ConsumeEnrollmentToken(ctx, node.NodeID, tokenHash, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("consume enrollment token: %w", err)
	}
	if !consumed {
		s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "failed", slog.LevelWarn, map[string]any{
			"node_id": node.NodeID,
			"reason":  "invalid_or_consumed_token",
		})
		return nil, fmt.Errorf("%w: %s", ErrInvalidNodeToken, node.NodeID)
	}

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
		s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "failed", slog.LevelError, map[string]any{
			"node_id": node.NodeID,
			"reason":  "certificate_issue_after_consume",
			"error":   err.Error(),
		})
		return nil, fmt.Errorf("%w: %w", ErrEnrollmentIssueAfterConsume, err)
	}

	if err := s.persistCertBinding(ctx, node.NodeID, result, previousFingerprint); err != nil {
		// Files were published but the DB binding failed: restore the old pair so
		// disk and DB remain consistent. The OTP stays consumed (documented UX).
		if rbErr := RollbackCertPair(result.CertPath, result.KeyPath); rbErr != nil {
			s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "failed", slog.LevelError, map[string]any{
				"node_id": node.NodeID,
				"reason":  "cert_pair_rollback_failed",
				"error":   rbErr.Error(),
			})
		}
		return nil, err
	}
	CommitCertPair(result.CertPath, result.KeyPath)

	enrollment, err := s.buildEnrollmentFromIssue(node.NodeID, serverAddr, result)
	if err != nil {
		return nil, err
	}

	if previousFingerprint != "" {
		s.auditSvc.LogSecurity(ctx, "NODE_REENROLL", "success", slog.LevelWarn, map[string]any{
			"node_id":              node.NodeID,
			"previous_fingerprint": fingerprintPrefix(previousFingerprint),
		})
	}

	s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "success", slog.LevelInfo, map[string]any{
		"node_id":      node.NodeID,
		"cert_subject": result.Subject,
		"reenroll":     previousFingerprint != "",
	})

	return enrollment, nil
}

func (s *Service) resolveEnrollNode(ctx context.Context, nodeID string) (model.Node, error) {
	nodeID, err := validateNodeID(nodeID)
	if err != nil {
		s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "failed", slog.LevelWarn, map[string]any{
			"reason": "missing_node_id",
		})
		return model.Node{}, err
	}

	node, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "failed", slog.LevelWarn, map[string]any{
			"node_id": nodeID,
			"reason":  "node_not_found",
		})
		if errors.Is(err, contract.ErrNotFound) {
			return model.Node{}, ErrNodeNotFound
		}
		return model.Node{}, fmt.Errorf("get node %q for enrollment: %w", nodeID, err)
	}
	return node, nil
}

func (s *Service) verifyEnrollmentToken(ctx context.Context, node model.Node, token string) error {
	if node.EnrollmentTokenHash == "" {
		s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "failed", slog.LevelWarn, map[string]any{
			"node_id": node.NodeID,
			"reason":  "missing_token",
		})
		return fmt.Errorf("%w: %s", ErrMissingNodeToken, node.NodeID)
	}
	hash := hashEnrollmentToken(token)
	if subtle.ConstantTimeCompare([]byte(node.EnrollmentTokenHash), []byte(hash)) != 1 {
		s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "failed", slog.LevelWarn, map[string]any{
			"node_id": node.NodeID,
			"reason":  "invalid_token",
		})
		return fmt.Errorf("%w: %s", ErrInvalidNodeToken, node.NodeID)
	}
	if node.EnrollmentTokenExpiresAt.IsZero() {
		s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "failed", slog.LevelWarn, map[string]any{
			"node_id": node.NodeID,
			"reason":  "token_expired",
		})
		return fmt.Errorf("%w: %s", ErrExpiredNodeToken, node.NodeID)
	}
	if time.Now().UTC().After(node.EnrollmentTokenExpiresAt) {
		s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "failed", slog.LevelWarn, map[string]any{
			"node_id": node.NodeID,
			"reason":  "token_expired",
		})
		return fmt.Errorf("%w: %s", ErrExpiredNodeToken, node.NodeID)
	}
	return nil
}

func (s *Service) persistCertBinding(ctx context.Context, nodeID string, result *CertResult, previousFingerprint string) error {
	expiresAt, err := time.Parse(time.RFC3339, result.ExpiresAt)
	if err != nil {
		return fmt.Errorf("parse certificate expiry: %w", err)
	}
	patch := certFingerprintPatch(previousFingerprint, result.Fingerprint, result.Subject, expiresAt)
	if err := s.nodeRepo.UpdateCertFields(ctx, nodeID, patch); err != nil {
		s.auditSvc.LogSecurity(ctx, "NODE_ENROLL", "failed", slog.LevelError, map[string]any{
			"node_id": nodeID,
			"reason":  "db_save_failed_after_consume",
			"error":   err.Error(),
		})
		return fmt.Errorf("%w: persist cert binding for node %q: %v", ErrEnrollmentIssueAfterConsume, nodeID, err)
	}
	return nil
}

func (s *Service) buildEnrollmentFromIssue(nodeID, serverAddr string, result *CertResult) (*Enrollment, error) {
	if len(result.CertPEM) == 0 || len(result.KeyPEM) == 0 {
		return nil, fmt.Errorf("issued certificate materials missing from memory")
	}
	caPEM, err := os.ReadFile(s.clusterCACertPath())
	if err != nil {
		return nil, fmt.Errorf("read cluster CA: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, result.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("parse certificate expiry: %w", err)
	}

	return &Enrollment{
		NodeID:        nodeID,
		ServerAddr:    serverAddr,
		CertPEM:       string(result.CertPEM),
		KeyPEM:        string(result.KeyPEM),
		CAPEM:         string(caPEM),
		CertExpiresAt: expiresAt,
	}, nil
}

func (s *Service) CreateClusterCert(ctx context.Context, req CreateClusterCertRequest) (*CertResult, error) {
	nodeID, err := validateNodeID(req.NodeID)
	if err != nil {
		return nil, err
	}

	lock := s.nodeCertLock(nodeID)
	lock.Lock()
	defer lock.Unlock()

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
			_ = RollbackCertPair(result.CertPath, result.KeyPath)
			return nil, fmt.Errorf("parse certificate expiry: %w", err)
		}
		if _, err := s.UpdateCert(ctx, result.CommonName, result.Fingerprint, result.Subject, expiresAt); err != nil {
			_ = RollbackCertPair(result.CertPath, result.KeyPath)
			return nil, err
		}
	}
	CommitCertPair(result.CertPath, result.KeyPath)

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
		DialHost:         form.DialHost,
		Description:      strings.TrimSpace(form.Description),
		Groups:           model.NormalizeNodeGroups(form.Groups),
		AllowConnections: true,
		MaxConnections:   form.MaxConnections,
	}
	if form.AllowConnections != nil {
		node.AllowConnections = *form.AllowConnections
	}
	return node
}

func normalizeDialHost(input string) (string, error) {
	host := input
	if host == "" {
		return "", nil
	}
	if err := hostpkg.ValidateHost(host); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidDialHost, err)
	}
	return host, nil
}

// nodeCertLock serializes certificate/key writes per node within this process.
// The map is intentionally never evicted: node rows are not deleted in the current
// lifecycle, so it is bounded by cluster node count, and deleting a live lock entry
// would let two goroutines mutate the same cert pair concurrently. Cross-instance
// serialization is provided by the CAS consume in EnrollWithToken, not by this lock.
func (s *Service) nodeCertLock(nodeID string) *sync.Mutex {
	v, _ := s.nodeCertLocks.LoadOrStore(nodeID, &sync.Mutex{})
	return v.(*sync.Mutex)
}
