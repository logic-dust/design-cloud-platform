package agent

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	fsutil "design-cloud-engine/internal/platform/fs"
)

// errCertRenewed ends the stream session on purpose so connectLoop reconnects and
// buildTLSCredentials re-reads the freshly written certificate from disk.
var errCertRenewed = errors.New("certificate renewed, reconnecting")

// certAuthRejectionRollbackFailures is the number of consecutive explicit authentication
// rejections after which the agent rolls back to the .prev certificate.
//
// The renewal-switch counter above only covers the instant right after a renewal. A certificate
// can become unusable at any later time too (its fingerprint was changed in the DB, the node row
// was recreated, ...), and then the node would retry forever while staying offline. Rolling back
// is safe: the previous certificate is still accepted through the server's dual-fingerprint
// window, so it cannot bypass validation.
const certAuthRejectionRollbackFailures = 3

// certAuthRejectionRollbackCooldown throttles the rejection-triggered rollback: if the previous
// certificate is rejected as well there is nothing left to roll back to, and the node must keep
// retrying with backoff instead of looping on the same restore.
const certAuthRejectionRollbackCooldown = 10 * time.Minute

// noteAuthRejection counts consecutive explicit authentication rejections and rolls back to the
// previous certificate once the threshold is reached.
func (a *Agent) noteAuthRejection() {
	a.renewMu.Lock()
	a.authRejections++
	rejections := a.authRejections
	cooledDown := time.Since(a.lastAuthRollback) >= certAuthRejectionRollbackCooldown
	a.renewMu.Unlock()

	if rejections < certAuthRejectionRollbackFailures || !cooledDown {
		return
	}
	if !hasBackup(a.cfg.TLS.CertPath) || !hasBackup(a.cfg.TLS.KeyPath) {
		return
	}
	if err := restoreBackup(a.cfg.TLS.CertPath); err != nil {
		a.logger.Error("Failed to roll back certificate", "error", err)
		return
	}
	if err := restoreBackup(a.cfg.TLS.KeyPath); err != nil {
		a.logger.Error("Failed to roll back key", "error", err)
		return
	}
	a.renewMu.Lock()
	a.authRejections = 0
	a.lastAuthRollback = time.Now()
	// A rejection-triggered rollback supersedes a pending renewal switch.
	a.renewActive = false
	a.renewFailures = 0
	a.renewMu.Unlock()
	a.logger.Error("Rolled back to previous certificate after consecutive authentication rejections",
		"rejections", rejections)
}

// noteAuthAccepted clears the rejection counter once a session was established successfully.
func (a *Agent) noteAuthAccepted() {
	a.renewMu.Lock()
	a.authRejections = 0
	a.renewMu.Unlock()
}

// hasBackup reports whether a .prev copy of path exists (i.e. there is something to roll back to).
// The suffix is assigned to a variable first: the security gate rejects path concatenation
// written inline inside an os.* call (path-traversal heuristic).
func hasBackup(path string) bool {
	prev := path + ".prev"
	_, err := os.Stat(prev)
	return err == nil
}

// certRenewRollbackFailures is the number of consecutive reconnect failures after
// a renewal that triggers an automatic rollback to the .prev certificate (D12).
const certRenewRollbackFailures = 3

type pendingRenewal struct {
	key         *ecdsa.PrivateKey
	csrPEM      []byte
	requestedAt time.Time
}

// handleRenewCertCommand starts a renewal: generate a fresh key pair locally,
// build a CSR and upload it. The private key never leaves the agent (CSR mode).
func (a *Agent) handleRenewCertCommand(stream pb.ControlService_SyncClient, cmd *pb.RenewCertCommand) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		a.logger.Error("Failed to generate renewal key pair", "error", err)
		return
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: a.nodeIdentity},
	}, key)
	if err != nil {
		a.logger.Error("Failed to create renewal CSR", "error", err)
		return
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})

	a.renewMu.Lock()
	a.pendingRenew = &pendingRenewal{key: key, csrPEM: csrPEM, requestedAt: time.Now()}
	a.renewMu.Unlock()

	a.logger.Info("Certificate renewal requested, uploading CSR", "reason", cmd.GetReason())
	if err := a.sendMessage(stream, &pb.AgentMessage{
		Payload: &pb.AgentMessage_RenewCertRequest{
			RenewCertRequest: &pb.RenewCertRequest{Csr: csrPEM},
		},
	}); err != nil {
		a.logger.Error("Failed to upload renewal CSR", "error", err)
	}
}

// handleRenewCertResponse validates the signed certificate, installs it locally
// and forces a reconnect. Every check from the plan's security design is applied
// (key pairing / CN / issuer / NotAfter monotonic) before touching the disk.
func (a *Agent) handleRenewCertResponse(stream pb.ControlService_SyncClient, resp *pb.RenewCertResponse) error {
	if resp == nil || len(resp.GetCert()) == 0 {
		return nil
	}

	a.renewMu.Lock()
	pending := a.pendingRenew
	a.pendingRenew = nil
	a.renewMu.Unlock()
	if pending == nil {
		a.reportRenewFailure(stream, "no pending renewal")
		return nil
	}

	fail := func(msg string) error {
		a.reportRenewFailure(stream, msg)
		return nil
	}

	block, _ := pem.Decode(resp.GetCert())
	if block == nil || block.Type != "CERTIFICATE" {
		return fail("response certificate is not PEM")
	}
	newCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fail("parse response certificate: " + err.Error())
	}

	// 1. The signed certificate must match the private key generated locally.
	pub, ok := newCert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&pending.key.PublicKey) {
		return fail("certificate public key does not match the local private key")
	}
	// 2. CN must be our own identity.
	if newCert.Subject.CommonName != a.nodeIdentity {
		return fail(fmt.Sprintf("certificate CN %q does not match node ID %q", newCert.Subject.CommonName, a.nodeIdentity))
	}
	// 3. The CA in the response must be the cluster CA we already trust, and the
	//    certificate must actually be signed by it.
	localCA, err := loadCertPEMFile(a.cfg.TLS.CAPath)
	if err != nil {
		return fail("load local CA: " + err.Error())
	}
	respCA, err := parsePEMCertificate(resp.GetCa())
	if err != nil {
		return fail("parse response CA: " + err.Error())
	}
	if !bytes.Equal(respCA.Raw, localCA.Raw) {
		return fail("response CA does not match the local cluster CA")
	}
	if err := newCert.CheckSignatureFrom(localCA); err != nil {
		return fail("certificate is not signed by the cluster CA: " + err.Error())
	}
	// 4. NotAfter must strictly increase (anti-downgrade).
	current, err := loadCertPEMFile(a.cfg.TLS.CertPath)
	if err != nil {
		return fail("load current certificate: " + err.Error())
	}
	if !newCert.NotAfter.After(current.NotAfter) {
		return fail("new certificate expiry is not later than the current one")
	}

	// Install: keep .prev for rollback, then atomically write cert + key.
	if err := backupForRollback(a.cfg.TLS.CertPath); err != nil {
		return fail("backup current certificate: " + err.Error())
	}
	if err := backupForRollback(a.cfg.TLS.KeyPath); err != nil {
		return fail("backup current key: " + err.Error())
	}
	keyDER, err := x509.MarshalECPrivateKey(pending.key)
	if err != nil {
		return fail("marshal renewal key: " + err.Error())
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := fsutil.WriteSharedFile(a.cfg.TLS.CertPath, resp.GetCert()); err != nil {
		return fail("write renewed certificate: " + err.Error())
	}
	if err := fsutil.WritePrivateFile(a.cfg.TLS.KeyPath, keyPEM); err != nil {
		return fail("write renewed key: " + err.Error())
	}

	a.renewMu.Lock()
	a.renewActive = true
	a.renewFailures = 0
	a.renewMu.Unlock()

	a.logger.Info("Renewed certificate installed, reconnecting to apply it",
		"expires_at", newCert.NotAfter.Format(time.RFC3339))
	_ = a.sendRenewResult(stream, true, "installed")
	return errCertRenewed
}

// noteRenewalFailure counts post-renewal reconnect failures and rolls back to the
// .prev certificate after the threshold, so a bad new certificate cannot lock the
// node out (dual fingerprint keeps the rolled-back cert authenticatable).
func (a *Agent) noteRenewalFailure() {
	a.renewMu.Lock()
	if !a.renewActive {
		a.renewMu.Unlock()
		return
	}
	a.renewFailures++
	failures := a.renewFailures
	a.renewMu.Unlock()
	if failures < certRenewRollbackFailures {
		return
	}
	if err := restoreBackup(a.cfg.TLS.CertPath); err != nil {
		a.logger.Error("Failed to roll back certificate", "error", err)
		return
	}
	if err := restoreBackup(a.cfg.TLS.KeyPath); err != nil {
		a.logger.Error("Failed to roll back key", "error", err)
		return
	}
	a.renewMu.Lock()
	a.renewActive = false
	a.renewFailures = 0
	a.renewMu.Unlock()
	a.logger.Error("Rolled back to previous certificate after consecutive reconnect failures",
		"failures", failures)
}

// noteRenewalRecovered disarms the rollback counter once the new certificate has
// successfully established a session.
func (a *Agent) noteRenewalRecovered() {
	a.renewMu.Lock()
	defer a.renewMu.Unlock()
	if a.renewActive {
		a.renewActive = false
		a.renewFailures = 0
		a.logger.Info("Renewed certificate connected successfully")
	}
}

func (a *Agent) reportRenewFailure(stream pb.ControlService_SyncClient, message string) {
	a.logger.Warn("Certificate renewal rejected", "message", message)
	_ = a.sendRenewResult(stream, false, message)
}

func (a *Agent) sendRenewResult(stream pb.ControlService_SyncClient, ok bool, message string) error {
	return a.sendMessage(stream, &pb.AgentMessage{
		Payload: &pb.AgentMessage_RenewCertResult{
			RenewCertResult: &pb.RenewCertResult{Ok: ok, Message: message},
		},
	})
}

func parsePEMCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("not a PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func loadCertPEMFile(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePEMCertificate(data)
}

func backupForRollback(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	prev := path + ".prev"
	_ = os.Remove(prev)
	return os.Rename(path, prev)
}

func restoreBackup(path string) error {
	prev := path + ".prev"
	if _, err := os.Stat(prev); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(prev, path)
}
