package node

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"design-cloud-engine/internal/model"
)

type stubConnectionChecker map[string]bool

func (s stubConnectionChecker) IsConnected(nodeID string) bool {
	return s[nodeID]
}

func TestMaintenanceExitNeverEnrolledClearsRuntimeStatus(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()

	if err := repos.Node.Create(ctx, &model.Node{
		NodeID: "pre-enroll",
		Status: model.NodeStatusMaintenance,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	exit := false
	if _, err := svc.Update(ctx, "pre-enroll", UpdateNodeRequest{Maintenance: &exit}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := repos.Node.GetByNodeID(ctx, "pre-enroll")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.NodeStatusUnset {
		t.Fatalf("status = %v, want unset before first connect", got.Status)
	}
}

func TestMaintenanceExitPrefersActiveConnection(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	svc.connChecker = stubConnectionChecker{"live-node": true}
	ctx := context.Background()

	if err := repos.Node.Create(ctx, &model.Node{
		NodeID:          "live-node",
		Status:          model.NodeStatusMaintenance,
		LastConnectedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	exit := false
	if _, err := svc.Update(ctx, "live-node", UpdateNodeRequest{Maintenance: &exit}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := repos.Node.GetByNodeID(ctx, "live-node")
	if got.Status != model.NodeStatusOnline {
		t.Fatalf("status = %v, want online when registry reports connected", got.Status)
	}
}

func TestEnrollmentTokenConcurrentConsume(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()

	const nodeID = "concurrent-bootstrap"
	_, issued, err := svc.RegisterNodeFromInput(ctx, ReqCreateNodeForm{NodeID: nodeID})
	if err != nil || issued == nil {
		t.Fatalf("register: %v", err)
	}
	token := issued.Token

	var wg sync.WaitGroup
	type enrollOutcome struct {
		enrollment *Enrollment
		err        error
	}
	outcomes := make(chan enrollOutcome, 2)
	for range 2 {
		wg.Go(func() {
			e, err := svc.EnrollWithToken(ctx, nodeID, token, "")
			outcomes <- enrollOutcome{e, err}
		})
	}
	wg.Wait()
	close(outcomes)

	var success, fail int
	var winner *Enrollment
	for o := range outcomes {
		if o.err == nil {
			success++
			winner = o.enrollment
		} else {
			fail++
		}
	}
	if success != 1 || fail != 1 {
		t.Fatalf("expected exactly one bootstrap success and one failure, got success=%d fail=%d", success, fail)
	}
	if winner == nil || winner.CertPEM == "" || winner.KeyPEM == "" {
		t.Fatal("winner must return certificate materials")
	}

	reloaded, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.EnrollmentTokenHash != "" {
		t.Fatal("token hash must remain cleared after successful enrollment")
	}
	block, _ := pem.Decode([]byte(winner.CertPEM))
	if block == nil {
		t.Fatal("winner cert PEM invalid")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse winner cert: %v", err)
	}
	fp := fingerprintSHA256(cert)
	if reloaded.CertFingerprint != fp {
		t.Fatalf("DB fingerprint %q != winner cert %q", reloaded.CertFingerprint, fp)
	}
	onDisk, err := os.ReadFile(svc.clusterNodeCertPath(nodeID))
	if err != nil {
		t.Fatalf("read on-disk cert: %v", err)
	}
	diskBlock, _ := pem.Decode(onDisk)
	if diskBlock == nil {
		t.Fatal("on-disk cert invalid")
	}
	diskCert, err := x509.ParseCertificate(diskBlock.Bytes)
	if err != nil {
		t.Fatalf("parse disk cert: %v", err)
	}
	if fingerprintSHA256(diskCert) != fp {
		t.Fatal("on-disk cert fingerprint mismatch with API response")
	}
}

func TestEnrollWithToken_RejectsZeroExpiry(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()
	const nodeID = "zero-expiry-node"
	if err := repos.Node.Create(ctx, &model.Node{NodeID: nodeID, Status: model.NodeStatusUnset}); err != nil {
		t.Fatalf("create: %v", err)
	}
	plain, hash, _ := issueEnrollmentToken(time.Now())
	if err := repos.Node.SetEnrollmentToken(ctx, nodeID, hash, time.Time{}); err != nil {
		t.Fatalf("set token: %v", err)
	}
	if _, err := svc.EnrollWithToken(ctx, nodeID, plain, ""); !errors.Is(err, ErrExpiredNodeToken) {
		t.Fatalf("got %v, want ErrExpiredNodeToken", err)
	}
}

func TestEnrollConcurrentWithCreateClusterCert(t *testing.T) {
	svc, repos, _ := newTestNodeService(t)
	ctx := context.Background()
	const nodeID = "enroll-vs-admin"
	_, issued, err := svc.RegisterNodeFromInput(ctx, ReqCreateNodeForm{NodeID: nodeID})
	if err != nil || issued == nil {
		t.Fatalf("register: %v", err)
	}

	var wg sync.WaitGroup
	type outcome struct {
		kind string
		err  error
	}
	ch := make(chan outcome, 2)
	wg.Go(func() {
		_, err := svc.EnrollWithToken(ctx, nodeID, issued.Token, "")
		ch <- outcome{"enroll", err}
	})
	wg.Go(func() {
		_, err := svc.CreateClusterCert(ctx, CreateClusterCertRequest{NodeID: nodeID, SyncNode: true})
		ch <- outcome{"admin", err}
	})
	wg.Wait()
	close(ch)

	var enrollErr, adminErr error
	for o := range ch {
		switch o.kind {
		case "enroll":
			enrollErr = o.err
		case "admin":
			adminErr = o.err
		}
	}
	// Both may succeed serially under the mutex; cert on disk must match DB fingerprint.
	if enrollErr != nil && adminErr != nil {
		t.Fatalf("both failed: enroll=%v admin=%v", enrollErr, adminErr)
	}
	node, err := repos.Node.GetByNodeID(ctx, nodeID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if node.CertFingerprint == "" {
		t.Fatal("expected a cert fingerprint after concurrent issue")
	}
	onDisk, err := os.ReadFile(svc.clusterNodeCertPath(nodeID))
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	block, _ := pem.Decode(onDisk)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fingerprintSHA256(cert) != node.CertFingerprint {
		t.Fatal("disk cert fingerprint diverged from DB after concurrent issue")
	}
}
