package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
)

type renewTestPKI struct {
	caCertPEM  []byte
	caKey      *ecdsa.PrivateKey
	caCert     *x509.Certificate
	certPath   string
	keyPath    string
	caPath     string
	currentKey *ecdsa.PrivateKey
}

func newRenewTestPKI(t *testing.T) *renewTestPKI {
	t.Helper()
	dir := t.TempDir()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}

	pki := &renewTestPKI{caKey: caKey, caCert: caCert}
	pki.caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	pki.caPath = filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(pki.caPath, pki.caCertPEM, 0644); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	// Current certificate (CN=node-1) valid for 1 year.
	currentCert, currentKey := pki.issue(t, "node-1", time.Now().Add(365*24*time.Hour))
	pki.certPath = filepath.Join(dir, "node1.crt")
	pki.keyPath = filepath.Join(dir, "node1.key")
	pki.currentKey = currentKey
	if err := os.WriteFile(pki.certPath, currentCert, 0644); err != nil {
		t.Fatalf("write current cert: %v", err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(currentKey)
	if err := os.WriteFile(pki.keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatalf("write current key: %v", err)
	}
	return pki
}

func (p *renewTestPKI) issue(t *testing.T, cn string, notAfter time.Time) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatalf("sign leaf: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key
}

func (p *renewTestPKI) newAgent() *Agent {
	return NewAgent(nil, Config{
		NodeID: "node-1",
		TLS: TLSClientConfig{
			CertPath: p.certPath,
			KeyPath:  p.keyPath,
			CAPath:   p.caPath,
		},
	}, slog.Default(), NewSettingsManager())
}

// 完整续签安装路径：配对校验通过 → 备份 .prev → 写入新证书 → 回报成功 → 断开重连。
func TestHandleRenewCertResponseInstallsCertificate(t *testing.T) {
	pki := newRenewTestPKI(t)
	agent := pki.newAgent()

	newKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate pending key: %v", err)
	}
	newCertPEM, _ := pki.issueWithKey(t, "node-1", time.Now().Add(2*365*24*time.Hour), newKey)
	agent.pendingRenew = &pendingRenewal{key: newKey, requestedAt: time.Now()}

	stream := &fakeSyncClient{}
	err = agent.handleRenewCertResponse(stream, &pb.RenewCertResponse{Cert: newCertPEM, Ca: pki.caCertPEM})
	if !errors.Is(err, errCertRenewed) {
		t.Fatalf("expected errCertRenewed, got %v", err)
	}
	if !agent.renewActive {
		t.Fatal("rollback guard must be armed after installation")
	}

	if _, err := os.Stat(pki.certPath + ".prev"); err != nil {
		t.Fatalf("previous certificate backup missing: %v", err)
	}
	installed, err := os.ReadFile(pki.certPath)
	if err != nil {
		t.Fatalf("read installed cert: %v", err)
	}
	if string(installed) != string(newCertPEM) {
		t.Fatal("installed certificate does not match the signed one")
	}
	if len(stream.sent) == 0 || !stream.sent[len(stream.sent)-1].GetRenewCertResult().GetOk() {
		t.Fatal("agent must report a successful install")
	}
}

// 配对校验：证书公钥与本地新生私钥不一致 → 拒绝，不落盘。
func TestHandleRenewCertResponseRejectsKeyMismatch(t *testing.T) {
	pki := newRenewTestPKI(t)
	agent := pki.newAgent()

	otherKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	agent.pendingRenew = &pendingRenewal{key: otherKey, requestedAt: time.Now()}
	mismatchedCert, _ := pki.issue(t, "node-1", time.Now().Add(2*365*24*time.Hour))

	stream := &fakeSyncClient{}
	if err := agent.handleRenewCertResponse(stream, &pb.RenewCertResponse{Cert: mismatchedCert, Ca: pki.caCertPEM}); err != nil {
		t.Fatalf("mismatch must be reported, not returned as fatal error: %v", err)
	}
	if agent.renewActive {
		t.Fatal("rollback guard must not arm on rejection")
	}
	last := stream.sent[len(stream.sent)-1].GetRenewCertResult()
	if last == nil || last.GetOk() {
		t.Fatal("agent must report failure on key mismatch")
	}
	if _, err := os.Stat(pki.certPath + ".prev"); !os.IsNotExist(err) {
		t.Fatal("no backup must be created on rejection")
	}
}

// 防降级：新证书 NotAfter 不晚于当前证书 → 拒绝。
func TestHandleRenewCertResponseRejectsDowngrade(t *testing.T) {
	pki := newRenewTestPKI(t)
	agent := pki.newAgent()

	newKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	agent.pendingRenew = &pendingRenewal{key: newKey, requestedAt: time.Now()}
	// Shorter validity than the current certificate (1 year).
	olderCert, _ := pki.issueWithKey(t, "node-1", time.Now().Add(30*24*time.Hour), newKey)

	stream := &fakeSyncClient{}
	if err := agent.handleRenewCertResponse(stream, &pb.RenewCertResponse{Cert: olderCert, Ca: pki.caCertPEM}); err != nil {
		t.Fatalf("downgrade must be rejected without fatal error: %v", err)
	}
	last := stream.sent[len(stream.sent)-1].GetRenewCertResult()
	if last == nil || last.GetOk() {
		t.Fatal("agent must report failure on downgrade")
	}
}

// 回退：连续失败达到阈值后恢复 .prev 证书。
func TestNoteRenewalFailureRollsBack(t *testing.T) {
	pki := newRenewTestPKI(t)
	agent := pki.newAgent()

	originalCert, err := os.ReadFile(pki.certPath)
	if err != nil {
		t.Fatalf("read original cert: %v", err)
	}
	if err := backupForRollback(pki.certPath); err != nil {
		t.Fatalf("backup cert: %v", err)
	}
	if err := backupForRollback(pki.keyPath); err != nil {
		t.Fatalf("backup key: %v", err)
	}
	if err := os.WriteFile(pki.certPath, []byte("broken-cert"), 0644); err != nil {
		t.Fatalf("simulate broken cert: %v", err)
	}

	agent.renewMu.Lock()
	agent.renewActive = true
	agent.renewMu.Unlock()

	for i := 0; i < certRenewRollbackFailures; i++ {
		agent.noteRenewalFailure()
	}

	restored, err := os.ReadFile(pki.certPath)
	if err != nil {
		t.Fatalf("read restored cert: %v", err)
	}
	if string(restored) != string(originalCert) {
		t.Fatal("rollback must restore the previous certificate")
	}
	if agent.renewActive {
		t.Fatal("rollback guard must be disarmed after restore")
	}
}

// An explicit authentication rejection must roll the node back to .prev even when the
// renewal-switch guard is not armed: that is the case of a certificate invalidated long
// after its renewal (fingerprint changed in the DB).
func TestNoteAuthRejectionRollsBackToPrev(t *testing.T) {
	pki := newRenewTestPKI(t)
	agent := pki.newAgent()

	originalCert, err := os.ReadFile(pki.certPath)
	if err != nil {
		t.Fatalf("read original cert: %v", err)
	}
	if err := backupForRollback(pki.certPath); err != nil {
		t.Fatalf("backup cert: %v", err)
	}
	if err := backupForRollback(pki.keyPath); err != nil {
		t.Fatalf("backup key: %v", err)
	}
	if err := os.WriteFile(pki.certPath, []byte("broken-cert"), 0644); err != nil {
		t.Fatalf("simulate broken cert: %v", err)
	}

	for i := 0; i < certAuthRejectionRollbackFailures; i++ {
		agent.noteAuthRejection()
	}

	restored, err := os.ReadFile(pki.certPath)
	if err != nil {
		t.Fatalf("read restored cert: %v", err)
	}
	if string(restored) != string(originalCert) {
		t.Fatal("rejection-triggered rollback must restore the previous certificate")
	}
	if agent.authRejections != 0 {
		t.Fatalf("rejection counter must reset after rollback, got %d", agent.authRejections)
	}
}

// A single rejection (below the threshold) must not touch the certificate.
func TestNoteAuthRejectionBelowThresholdKeepsCertificate(t *testing.T) {
	pki := newRenewTestPKI(t)
	agent := pki.newAgent()

	if err := backupForRollback(pki.certPath); err != nil {
		t.Fatalf("backup cert: %v", err)
	}
	// backupForRollback moves the file aside, so write the "current" certificate again.
	if err := os.WriteFile(pki.certPath, []byte("current-cert"), 0644); err != nil {
		t.Fatalf("write current cert: %v", err)
	}

	agent.noteAuthRejection()

	after, err := os.ReadFile(pki.certPath)
	if err != nil {
		t.Fatalf("read cert after rejection: %v", err)
	}
	if string(after) != "current-cert" {
		t.Fatal("a rejection below the threshold must not change the certificate")
	}
}

// Without a .prev copy there is nothing to roll back to: the agent must stay inert
// (and keep retrying with backoff) instead of failing hard.
func TestNoteAuthRejectionWithoutBackupIsInert(t *testing.T) {
	pki := newRenewTestPKI(t)
	agent := pki.newAgent()

	for i := 0; i < certAuthRejectionRollbackFailures; i++ {
		agent.noteAuthRejection()
	}

	if agent.renewActive {
		t.Fatal("a rollback without backup must not arm the renewal guard")
	}
	if _, err := os.Stat(pki.certPath); err != nil {
		t.Fatalf("certificate must stay in place, got %v", err)
	}
}

func (p *renewTestPKI) issueWithKey(t *testing.T, cn string, notAfter time.Time, key *ecdsa.PrivateKey) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatalf("sign leaf: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key
}
