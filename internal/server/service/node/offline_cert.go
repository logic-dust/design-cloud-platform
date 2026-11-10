package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// OfflineIssueCert performs offline certificate issuance directly on the server node.
// It generates or signs node certificates using the local cluster CA, updates database
// records (dual-fingerprint rotation; runtime status unchanged until first connect), and emits audit logs.
func (s *Service) OfflineIssueCert(ctx context.Context, req OfflineIssueCertRequest) (*OfflineIssueCertResult, error) {
	nodeID, err := validateNodeID(req.NodeID)
	if err != nil {
		return nil, err
	}

	hosts := req.Hosts
	if len(hosts) == 0 {
		hosts = []string{nodeID, "127.0.0.1", "localhost"}
	}

	validDays := req.ValidDays
	if validDays <= 0 {
		if req.IsServer {
			validDays = ControlNodeCertValidDays
		} else {
			validDays = AgentCertValidDays
		}
	} else if validDays > 3650 {
		return nil, fmt.Errorf("%w: validDays %d exceeds maximum limit 3650", ErrInvalidNodeCertOptions, validDays)
	}

	caCertFile := s.clusterCACertPath()
	caKeyFile := s.clusterCAKeyPath()
	caCertPEM, caKeyPEM, err := loadCAFiles(caCertFile, caKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load cluster CA: %w", err)
	}

	var isNewNode bool
	existing, err := s.nodeRepo.GetByNodeID(ctx, nodeID)
	if err != nil {
		if !errors.Is(err, contract.ErrNotFound) {
			return nil, fmt.Errorf("lookup node %q: %w", nodeID, err)
		}
		isNewNode = true
		newNode := model.Node{
			NodeID:           nodeID,
			IsServer:         req.IsServer,
			AllowConnections: true,
		}
		if err := s.nodeRepo.Create(ctx, &newNode); err != nil {
			return nil, fmt.Errorf("create node record %q: %w", nodeID, err)
		}
	} else if existing.CertFingerprint != "" && !req.Force {
		return nil, ErrNodeCertAlreadyExists
	}

	var certPEM, keyPEM []byte
	if len(req.CSRPEM) == 0 {
		certPEM, keyPEM, err = generateSignedCert(certParams{
			Hosts:      hosts,
			ValidFor:   time.Duration(validDays) * 24 * time.Hour,
			OrgName:    orgNode,
			CommonName: nodeID,
		}, caCertPEM, caKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("generate certificate: %w", err)
		}
	} else {
		certPEM, _, err = SignNodeCSR(CSRSignOptions{
			CSRPEM:     req.CSRPEM,
			CommonName: nodeID,
			Hosts:      hosts,
			ValidDays:  validDays,
			CACertFile: caCertFile,
			CAKeyFile:  caKeyFile,
		})
		if err != nil {
			return nil, fmt.Errorf("sign CSR: %w", err)
		}
	}

	parsed, err := parseCert(certPEM)
	if err != nil {
		return nil, fmt.Errorf("parse generated certificate: %w", err)
	}

	fp := fingerprintSHA256(parsed)
	subject := parsed.Subject.String()
	expiresAt := parsed.NotAfter

	updatedNode, err := s.UpdateCert(ctx, nodeID, fp, subject, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("update cert in database: %w", err)
	}

	_ = s.nodeRepo.SetEnrollmentToken(ctx, nodeID, "", time.Time{})
	_ = updatedNode

	s.auditSvc.LogNode(ctx, "NODE_CERT_OFFLINE_ISSUED", "success", nodeID, slog.LevelInfo, map[string]any{
		"is_server":   req.IsServer,
		"with_csr":    len(req.CSRPEM) > 0,
		"fingerprint": fp,
		"valid_days":  validDays,
		"is_new_node": isNewNode,
	})

	return &OfflineIssueCertResult{
		CertPEM:     certPEM,
		KeyPEM:      keyPEM,
		CAPEM:       caCertPEM,
		Fingerprint: fp,
		Subject:     subject,
		ExpiresAt:   expiresAt,
		IsNewNode:   isNewNode,
	}, nil
}
