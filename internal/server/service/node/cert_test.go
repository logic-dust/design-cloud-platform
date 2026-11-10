package node

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssueNodeCert_ValidDaysValidation(t *testing.T) {
	// Test case: validDays exceeds maximum limit (3650)
	opts := CertOptions{
		CommonName: "test-node",
		CACertFile: "ca.crt",
		CAKeyFile:  "ca.key",
		CertFile:   "node.crt",
		KeyFile:    "node.key",
		Hosts:      []string{"localhost"},
		ValidDays:  4000, // Exceeds 3650 limit
	}

	_, err := IssueNodeCert(opts)
	if err == nil {
		t.Fatal("expected error for excessive validDays, got nil")
	}

	if !errors.Is(err, ErrInvalidNodeCertOptions) {
		t.Errorf("expected error wrapping ErrInvalidNodeCertOptions, got: %v", err)
	}
}

// setupTestCA generates a fresh CA under dir and returns cert/key paths.
func setupTestCA(t *testing.T, dir string) (string, string) {
	t.Helper()
	caCert := filepath.Join(dir, "certs", "cluster", "ca.crt")
	caKey := filepath.Join(dir, "certs", "cluster", "ca", "ca.key")
	if _, err := InitializeCA(caCert, caKey, false); err != nil {
		t.Fatalf("InitializeCA: %v", err)
	}
	return caCert, caKey
}

func TestInitializeCA_ReuseAndIncomplete(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := setupTestCA(t, dir)

	// Pair exists → sentinel ErrCAExists (caller treats as no-op).
	if _, err := InitializeCA(caCert, caKey, false); !errors.Is(err, ErrCAExists) {
		t.Fatalf("expected ErrCAExists, got %v", err)
	}

	// Incomplete material must fail fast, never silently regenerate.
	if err := os.Remove(caKey); err != nil {
		t.Fatalf("remove CA key: %v", err)
	}
	if _, err := InitializeCA(caCert, caKey, false); err == nil || errors.Is(err, ErrCAExists) {
		t.Fatalf("expected incomplete-CA failure, got %v", err)
	}

	// force=true regenerates both materials.
	if _, err := InitializeCA(caCert, caKey, true); err != nil {
		t.Fatalf("forced InitializeCA: %v", err)
	}
	if !fileExists(caCert) || !fileExists(caKey) {
		t.Fatal("forced regeneration must write both materials")
	}
}

func TestInitializeCA_SplitDirectories(t *testing.T) {
	dir := t.TempDir()
	caCert := filepath.Join(dir, "certs", "cluster", "ca.crt")
	caKey := filepath.Join(dir, "certs", "cluster", "ca", "ca.key")

	// Must not fail with ENOENT: the key parent differs from the cert parent.
	if _, err := InitializeCA(caCert, caKey, false); err != nil {
		t.Fatalf("InitializeCA with split directories: %v", err)
	}
	if !fileExists(caCert) || !fileExists(caKey) {
		t.Fatal("both CA materials must exist")
	}
}

func TestIssueNodeCert_DefaultsToAgentValidity(t *testing.T) {
	if AgentCertValidDays != 730 {
		t.Fatalf("AgentCertValidDays = %d, want 730", AgentCertValidDays)
	}
	if ControlNodeCertValidDays != 1095 {
		t.Fatalf("ControlNodeCertValidDays = %d, want 1095", ControlNodeCertValidDays)
	}

	dir := t.TempDir()
	caCert, caKey := setupTestCA(t, dir)
	certPath := filepath.Join(dir, "certs", "cluster", "cn01", "cn01.crt")
	keyPath := filepath.Join(dir, "certs", "cluster", "cn01", "cn01.key")

	if _, err := IssueNodeCert(CertOptions{
		CommonName: "cn01",
		Hosts:      []string{"cn01"},
		CACertFile: caCert,
		CAKeyFile:  caKey,
		CertFile:   certPath,
		KeyFile:    keyPath,
	}); err != nil {
		t.Fatalf("IssueNodeCert: %v", err)
	}

	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read issued cert: %v", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("issued cert is not PEM")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse issued cert: %v", err)
	}
	got := parsed.NotAfter.Sub(parsed.NotBefore)
	want := time.Duration(AgentCertValidDays) * 24 * time.Hour
	if diff := got - want; diff > time.Minute || diff < -time.Minute {
		t.Fatalf("validity = %v, want %v", got, want)
	}
}

func TestPublishCertKeyPair_RestoresOnKeyWriteFailure(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "n.crt")
	keyPath := filepath.Join(dir, "n.key")
	oldCert, oldKey := []byte("OLD-CERT"), []byte("OLD-KEY-MATERIAL")
	if err := publishCertKeyPair(certPath, keyPath, oldCert, oldKey); err != nil {
		t.Fatalf("seed pair: %v", err)
	}

	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not-a-dir"), 0600); err != nil {
		t.Fatal(err)
	}
	badKey := filepath.Join(blocked, "n.key")

	if err := publishCertKeyPair(certPath, badKey, []byte("NEW-CERT"), []byte("NEW-KEY")); err == nil {
		t.Fatal("expected key write failure")
	}
	got, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	if string(got) != string(oldCert) {
		t.Fatalf("cert after failed publish = %q, want restored %q", got, oldCert)
	}
	gotKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if string(gotKey) != string(oldKey) {
		t.Fatalf("original key path changed unexpectedly: %q", gotKey)
	}
}

func TestPublishCertKeyPair_KeepsPrevForCommitWindow(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "n.crt")
	keyPath := filepath.Join(dir, "n.key")
	if err := publishCertKeyPair(certPath, keyPath, []byte("v1-cert"), []byte("v1-key")); err != nil {
		t.Fatalf("v1: %v", err)
	}
	if err := publishCertKeyPair(certPath, keyPath, []byte("v2-cert"), []byte("v2-key")); err != nil {
		t.Fatalf("v2: %v", err)
	}
	prevCert, err := os.ReadFile(certPath + certPairPrevSuffix)
	if err != nil {
		t.Fatalf("read cert.prev: %v", err)
	}
	if string(prevCert) != "v1-cert" {
		t.Fatalf("cert.prev = %q, want v1-cert", prevCert)
	}
	prevKey, err := os.ReadFile(keyPath + certPairPrevSuffix)
	if err != nil {
		t.Fatalf("read key.prev: %v", err)
	}
	if string(prevKey) != "v1-key" {
		t.Fatalf("key.prev = %q, want v1-key", prevKey)
	}
}

func TestCommitCertPair_RemovesSnapshots(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "n.crt")
	keyPath := filepath.Join(dir, "n.key")
	if err := publishCertKeyPair(certPath, keyPath, []byte("v1-cert"), []byte("v1-key")); err != nil {
		t.Fatalf("v1: %v", err)
	}
	if err := publishCertKeyPair(certPath, keyPath, []byte("v2-cert"), []byte("v2-key")); err != nil {
		t.Fatalf("v2: %v", err)
	}
	CommitCertPair(certPath, keyPath)
	for _, p := range []string{certPath + certPairPrevSuffix, keyPath + certPairPrevSuffix} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed after commit", p)
		}
	}
}

func TestRollbackCertPair_RestoresPreviousPair(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "n.crt")
	keyPath := filepath.Join(dir, "n.key")
	if err := publishCertKeyPair(certPath, keyPath, []byte("v1-cert"), []byte("v1-key")); err != nil {
		t.Fatalf("v1: %v", err)
	}
	if err := publishCertKeyPair(certPath, keyPath, []byte("v2-cert"), []byte("v2-key")); err != nil {
		t.Fatalf("v2: %v", err)
	}
	if err := RollbackCertPair(certPath, keyPath); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	cert, err := os.ReadFile(certPath)
	if err != nil || string(cert) != "v1-cert" {
		t.Fatalf("cert = %q, err=%v", cert, err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil || string(key) != "v1-key" {
		t.Fatalf("key = %q, err=%v", key, err)
	}
}

func newTestCSR(t *testing.T, cn string) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}, priv)
	if err != nil {
		t.Fatalf("create CSR: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func TestSignNodeCSR(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := setupTestCA(t, dir)

	csrPEM := newTestCSR(t, "cn01")
	certPEM, result, err := SignNodeCSR(CSRSignOptions{
		CSRPEM:     csrPEM,
		CommonName: "cn01",
		Hosts:      []string{"cn01"},
		CACertFile: caCert,
		CAKeyFile:  caKey,
	})
	if err != nil {
		t.Fatalf("SignNodeCSR: %v", err)
	}
	if result.CommonName != "cn01" || result.Fingerprint == "" || result.ExpiresAt == "" {
		t.Fatalf("unexpected result: %+v", result)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("signed cert is not PEM")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse signed cert: %v", err)
	}
	if parsed.Subject.CommonName != "cn01" {
		t.Fatalf("CN = %q, want cn01", parsed.Subject.CommonName)
	}
	if len(parsed.DNSNames) != 1 || parsed.DNSNames[0] != "cn01" {
		t.Fatalf("SANs = %v, want server-decided [cn01]", parsed.DNSNames)
	}

	// CN mismatch must be rejected.
	if _, _, err := SignNodeCSR(CSRSignOptions{
		CSRPEM: csrPEM, CommonName: "cn02",
		Hosts: []string{"cn02"}, CACertFile: caCert, CAKeyFile: caKey,
	}); err == nil {
		t.Fatal("expected CN mismatch rejection")
	}

	// Invalid PEM must be rejected.
	if _, _, err := SignNodeCSR(CSRSignOptions{
		CSRPEM: []byte("not-pem"), CommonName: "cn01",
		Hosts: []string{"cn01"}, CACertFile: caCert, CAKeyFile: caKey,
	}); err == nil {
		t.Fatal("expected invalid PEM rejection")
	}
}
