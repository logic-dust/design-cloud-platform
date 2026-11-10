package node

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"testing"

	"design-cloud-engine/internal/model"
)

func TestOfflineIssueCert_NewNode(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()

	const nodeID = "compute-offline-01"
	res, err := svc.OfflineIssueCert(ctx, OfflineIssueCertRequest{
		NodeID:   nodeID,
		IsServer: false,
	})
	if err != nil {
		t.Fatalf("OfflineIssueCert failed: %v", err)
	}

	if !res.IsNewNode {
		t.Errorf("expected IsNewNode to be true")
	}
	if len(res.CertPEM) == 0 {
		t.Errorf("expected non-empty CertPEM")
	}
	if len(res.KeyPEM) == 0 {
		t.Errorf("expected non-empty KeyPEM")
	}
	if len(res.CAPEM) == 0 {
		t.Errorf("expected non-empty CAPEM")
	}
	if res.Fingerprint == "" {
		t.Errorf("expected non-empty Fingerprint")
	}

	node, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if node.Status != model.NodeStatusUnset {
		t.Errorf("expected unset runtime status, got %v", node.Status)
	}
	if node.IsServer {
		t.Errorf("expected is_server to be false")
	}
	if node.CertFingerprint != res.Fingerprint {
		t.Errorf("expected node fingerprint %s, got %s", res.Fingerprint, node.CertFingerprint)
	}
	if node.EnrollmentTokenHash != "" {
		t.Errorf("expected enrollment token hash to be empty for offline-issued node")
	}
}

func TestOfflineIssueCert_ForceRotation(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()

	const nodeID = "compute-offline-02"
	res1, err := svc.OfflineIssueCert(ctx, OfflineIssueCertRequest{
		NodeID:   nodeID,
		IsServer: false,
	})
	if err != nil {
		t.Fatalf("first issue failed: %v", err)
	}

	// Re-issuing without force must be blocked
	_, err = svc.OfflineIssueCert(ctx, OfflineIssueCertRequest{
		NodeID:   nodeID,
		IsServer: false,
		Force:    false,
	})
	if !errors.Is(err, ErrNodeCertAlreadyExists) {
		t.Fatalf("expected ErrNodeCertAlreadyExists, got %v", err)
	}

	// Re-issuing with force must rotate fingerprint (dual fingerprint window)
	res2, err := svc.OfflineIssueCert(ctx, OfflineIssueCertRequest{
		NodeID:   nodeID,
		IsServer: false,
		Force:    true,
	})
	if err != nil {
		t.Fatalf("forced issue failed: %v", err)
	}

	if res2.IsNewNode {
		t.Errorf("expected IsNewNode to be false on rotation")
	}
	if res1.Fingerprint == res2.Fingerprint {
		t.Errorf("expected new fingerprint to differ from previous")
	}

	node, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if node.CertFingerprint != res2.Fingerprint {
		t.Errorf("expected current fingerprint %s, got %s", res2.Fingerprint, node.CertFingerprint)
	}
	if node.CertFingerprintPrev != res1.Fingerprint {
		t.Errorf("expected prev fingerprint %s, got %s", res1.Fingerprint, node.CertFingerprintPrev)
	}
}

func TestOfflineIssueCert_WithCSR(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()

	const nodeID = "compute-csr-01"

	// Generate external CSR
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	csrTemplate := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: nodeID},
	}
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, key)
	if err != nil {
		t.Fatalf("create csr: %v", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer})

	res, err := svc.OfflineIssueCert(ctx, OfflineIssueCertRequest{
		NodeID:    nodeID,
		IsServer:  true,
		CSRPEM:    csrPEM,
		ValidDays: 365,
	})
	if err != nil {
		t.Fatalf("issue with CSR failed: %v", err)
	}

	if len(res.KeyPEM) != 0 {
		t.Errorf("expected KeyPEM to be empty when CSR is provided, got %d bytes", len(res.KeyPEM))
	}
	if len(res.CertPEM) == 0 {
		t.Errorf("expected non-empty CertPEM")
	}

	node, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if !node.IsServer {
		t.Errorf("expected is_server to be true")
	}
	if node.CertFingerprint != res.Fingerprint {
		t.Errorf("fingerprint mismatch: got %s, want %s", node.CertFingerprint, res.Fingerprint)
	}
}
