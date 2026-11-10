package node

import (
	"cmp"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	fsutil "design-cloud-engine/internal/platform/fs"
)

const (
	caName       = "ca"
	orgCA        = "Design Cloud Engine Root CA"
	commonNameCA = "Design Cloud Engine CA"
	orgNode      = "Design Cloud Engine"

	// AgentCertValidDays is the single source of truth for agent node certificate
	// validity (2 years). The agent holds a client identity that is re-read from
	// disk on every connect, so renewal can be fully automated with zero downtime.
	AgentCertValidDays = 730

	// ControlNodeCertValidDays is the single source of truth for control node
	// certificate validity (3 years). The control node holds a listener identity
	// loaded once at process start; renewal relies on GetCertificate hot reload.
	ControlNodeCertValidDays = 1095
)

// CertOptions holds parameters for node certificate generation.
type CertOptions struct {
	CommonName string   // Common Name (CN) for the certificate
	Hosts      []string // SANs; defaults to [name, 127.0.0.1, localhost]
	ValidDays  int      // validity in days; defaults to 3 years

	CACertFile string
	CAKeyFile  string
	CertFile   string
	KeyFile    string
}

// CertResult contains metadata for the generated node certificate.
type CertResult struct {
	CommonName  string `json:"commonName"`
	CertPath    string `json:"certPath"`
	KeyPath     string `json:"keyPath"`
	Fingerprint string `json:"fingerprint"`
	Subject     string `json:"subject"`
	ExpiresAt   string `json:"expiresAt"`
	CertPEM     []byte `json:"-"` // in-memory for enrollment only; never serialize to admin API
	KeyPEM      []byte `json:"-"`
}

// CAResult contains metadata for a generated CA.
type CAResult struct {
	CertPath  string `json:"certPath"`
	KeyPath   string `json:"keyPath"`
	Subject   string `json:"subject"`
	ExpiresAt string `json:"expiresAt"`
}

// ============================================================================
// Public API
// ============================================================================

// InitializeCA generates or reuses the cluster Root CA (exactly one per cluster).
//
// Layout: ca.crt is flat under certs/cluster (0755, traversable by every node) while
// ca.key is nested under certs/cluster/ca (0700). The two parent directories must be
// created separately with different permissions — creating only the cert parent would
// make the key write fail with ENOENT, and 0700 on certs/cluster would lock out
// compute nodes on shared storage.
//
// Reuse semantics (force=false):
//   - ca.crt and ca.key exist as a pair → return ErrCAExists (caller treats as no-op);
//   - exactly one of them exists        → fail fast for manual intervention (an
//     interrupted write or permission anomaly); never silently regenerate, since
//     overwriting would instantly invalidate every issued node certificate;
//   - neither exists                    → generate.
func InitializeCA(caCertFile, caKeyFile string, force bool) (*CAResult, error) {
	if caCertFile == "" || caKeyFile == "" {
		return nil, fmt.Errorf("CA cert and key paths are required")
	}
	if err := fsutil.MkdirShared(filepath.Dir(caCertFile)); err != nil { // certs/cluster → 0755
		return nil, fmt.Errorf("failed to create CA cert dir: %w", err)
	}
	if err := fsutil.MkdirPrivate(filepath.Dir(caKeyFile)); err != nil { // certs/cluster/ca → 0700
		return nil, fmt.Errorf("failed to create CA key dir: %w", err)
	}

	// Reuse is only valid when BOTH materials exist as a pair.
	certExists, keyExists := fileExists(caCertFile), fileExists(caKeyFile)
	if !force {
		switch {
		case certExists && keyExists:
			return nil, ErrCAExists
		case certExists != keyExists:
			return nil, fmt.Errorf("incomplete CA material: cert=%t key=%t — manual intervention required", certExists, keyExists)
		}
	}

	certPEM, keyPEM, err := generateCA(certParams{
		OrgName:    orgCA,
		CommonName: commonNameCA,
		ValidFor:   3650 * 24 * time.Hour,
		Hosts:      []string{},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA: %w", err)
	}
	if err := publishCertKeyPair(caCertFile, caKeyFile, certPEM, keyPEM); err != nil {
		return nil, err
	}

	parsed, err := parseCert(certPEM)
	if err != nil {
		return nil, err
	}
	return &CAResult{
		CertPath:  caCertFile,
		KeyPath:   caKeyFile,
		Subject:   parsed.Subject.String(),
		ExpiresAt: parsed.NotAfter.UTC().Format(time.RFC3339),
	}, nil
}

// IssueNodeCert generates and signs a node certificate using the provided CA.
func IssueNodeCert(opts CertOptions) (*CertResult, error) {
	commonName := opts.CommonName
	if commonName == "" {
		return nil, fmt.Errorf("%w: CommonName is required", ErrInvalidNodeCertOptions)
	}
	if commonName == caName {
		return nil, fmt.Errorf("%w: CommonName %q is reserved", ErrInvalidNodeCertOptions, caName)
	}

	if opts.ValidDays > 3650 {
		return nil, fmt.Errorf("%w: validDays %d exceeds maximum allowed limit (3650)", ErrInvalidNodeCertOptions, opts.ValidDays)
	}

	caFile := opts.CACertFile
	caKeyFile := opts.CAKeyFile
	if caFile == "" || caKeyFile == "" {
		return nil, fmt.Errorf("%w: missing CA cert or key file paths", ErrInvalidNodeCertOptions)
	}

	caCertPEM, caKeyPEM, err := loadCAFiles(caFile, caKeyFile)
	if err != nil {
		return nil, err
	}

	hosts := opts.Hosts
	if len(hosts) == 0 {
		return nil, fmt.Errorf("%w: hosts (SANs) cannot be empty", ErrInvalidNodeCertOptions)
	}

	validDays := opts.ValidDays
	if validDays <= 0 {
		validDays = AgentCertValidDays
	}

	certPath := opts.CertFile
	keyPath := opts.KeyFile
	if certPath == "" || keyPath == "" {
		return nil, fmt.Errorf("%w: missing output cert or key file paths", ErrInvalidNodeCertOptions)
	}

	outDir := filepath.Dir(certPath)
	if err := fsutil.MkdirPrivate(outDir); err != nil { // <nodeID>/ → 0700
		return nil, fmt.Errorf("failed to create cert dir: %w", err)
	}
	keyDir := filepath.Dir(keyPath)
	if outDir != keyDir {
		if err := fsutil.MkdirPrivate(keyDir); err != nil {
			return nil, fmt.Errorf("failed to create key dir: %w", err)
		}
	}

	certPEM, keyPEM, err := generateSignedCert(certParams{
		Hosts:      hosts,
		ValidFor:   time.Duration(validDays) * 24 * time.Hour,
		OrgName:    orgNode,
		CommonName: commonName,
	}, caCertPEM, caKeyPEM)
	if err != nil {
		return nil, err
	}

	if err := publishCertKeyPair(certPath, keyPath, certPEM, keyPEM); err != nil {
		return nil, err
	}

	parsed, err := parseCert(certPEM)
	if err != nil {
		return nil, err
	}
	return &CertResult{
		CommonName:  commonName,
		CertPath:    certPath,
		KeyPath:     keyPath,
		Fingerprint: fingerprintSHA256(parsed),
		Subject:     parsed.Subject.String(),
		ExpiresAt:   parsed.NotAfter.UTC().Format(time.RFC3339),
		CertPEM:     append([]byte(nil), certPEM...),
		KeyPEM:      append([]byte(nil), keyPEM...),
	}, nil
}

// CSRSignOptions holds parameters for signing an externally generated CSR.
// The private key never reaches the server in this flow (auto-renewal path).
type CSRSignOptions struct {
	CSRPEM     []byte   // PEM-encoded certificate request ("CERTIFICATE REQUEST")
	CommonName string   // expected CN; must match the CSR subject CN
	Hosts      []string // server-decided SANs; CSR-declared SANs are ignored
	ValidDays  int      // validity in days; defaults to AgentCertValidDays

	CACertFile string
	CAKeyFile  string
}

// SignNodeCSR validates a PEM-encoded certificate request and returns a signed
// certificate. SANs are decided by the server (never taken from the CSR).
//
// Contract: pure signing, no filesystem writes — the agent owns the private key
// and writes the returned certificate locally.
func SignNodeCSR(opts CSRSignOptions) ([]byte, *CertResult, error) {
	commonName := opts.CommonName
	if commonName == "" {
		return nil, nil, fmt.Errorf("%w: CommonName is required", ErrInvalidNodeCertOptions)
	}
	if commonName == caName {
		return nil, nil, fmt.Errorf("%w: CommonName %q is reserved", ErrInvalidNodeCertOptions, caName)
	}
	if opts.ValidDays > 3650 {
		return nil, nil, fmt.Errorf("%w: validDays %d exceeds maximum allowed limit (3650)", ErrInvalidNodeCertOptions, opts.ValidDays)
	}
	if len(opts.Hosts) == 0 {
		return nil, nil, fmt.Errorf("%w: hosts (SANs) cannot be empty", ErrInvalidNodeCertOptions)
	}
	if opts.CACertFile == "" || opts.CAKeyFile == "" {
		return nil, nil, fmt.Errorf("%w: missing CA cert or key file paths", ErrInvalidNodeCertOptions)
	}

	block, _ := pem.Decode(opts.CSRPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("%w: CSR must be PEM encoded", ErrInvalidNodeCertOptions)
	}
	if block.Type != "CERTIFICATE REQUEST" && block.Type != "NEW CERTIFICATE REQUEST" {
		return nil, nil, fmt.Errorf("%w: unexpected PEM block %q", ErrInvalidNodeCertOptions, block.Type)
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: parse CSR: %v", ErrInvalidNodeCertOptions, err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, nil, fmt.Errorf("%w: CSR signature invalid: %v", ErrInvalidNodeCertOptions, err)
	}
	if csr.Subject.CommonName != commonName {
		return nil, nil, fmt.Errorf("%w: CSR CN %q does not match node ID %q", ErrInvalidNodeCertOptions, csr.Subject.CommonName, commonName)
	}

	caCertPEM, caKeyPEM, err := loadCAFiles(opts.CACertFile, opts.CAKeyFile)
	if err != nil {
		return nil, nil, err
	}
	caCert, caKey, err := parseCAKeyPair(caCertPEM, caKeyPEM)
	if err != nil {
		return nil, nil, err
	}

	validDays := opts.ValidDays
	if validDays <= 0 {
		validDays = AgentCertValidDays
	}

	template, err := createBaseTemplate(certParams{
		Hosts:      opts.Hosts,
		ValidFor:   time.Duration(validDays) * 24 * time.Hour,
		OrgName:    orgNode,
		CommonName: commonName,
	})
	if err != nil {
		return nil, nil, err
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, caCert, csr.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create signed certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	parsed, err := parseCert(certPEM)
	if err != nil {
		return nil, nil, err
	}
	return certPEM, &CertResult{
		CommonName:  commonName,
		Fingerprint: fingerprintSHA256(parsed),
		Subject:     parsed.Subject.String(),
		ExpiresAt:   parsed.NotAfter.UTC().Format(time.RFC3339),
	}, nil
}

// ============================================================================
// Private X.509 / PKI helpers (inlined from common/cert.go)
// ============================================================================

// certParams defines the low-level parameters for certificate generation.
type certParams struct {
	Hosts      []string
	ValidFor   time.Duration
	OrgName    string
	CommonName string
}

func createBaseTemplate(p certParams) (*x509.Certificate, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	notBefore := time.Now()
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{p.OrgName},
			CommonName:   p.CommonName,
		},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(p.ValidFor),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	for _, h := range p.Hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}
	return template, nil
}

// generateCA generates a CA certificate and private key, returning raw PEM bytes.
func generateCA(p certParams) (caCertPEM []byte, caKeyPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	template, err := createBaseTemplate(p)
	if err != nil {
		return nil, nil, err
	}

	template.IsCA = true
	template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	template.ExtKeyUsage = []x509.ExtKeyUsage{}
	template.Subject.CommonName = cmp.Or(template.Subject.CommonName, commonNameCA)

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	b, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to marshal ECDSA key: %w", err)
	}
	caKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: b})
	return caCertPEM, caKeyPEM, nil
}

// generateSignedCert generates a certificate signed by the provided CA.
func generateSignedCert(p certParams, caCertPEM, caKeyPEM []byte) (certPEM []byte, keyPEM []byte, err error) {
	caCert, caKey, err := parseCAKeyPair(caCertPEM, caKeyPEM)
	if err != nil {
		return nil, nil, err
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	template, err := createBaseTemplate(p)
	if err != nil {
		return nil, nil, err
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create signed certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	b, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to marshal ECDSA key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: b})
	return certPEM, keyPEM, nil
}

// parseCAKeyPair decodes the CA certificate and EC private key from PEM bytes.
func parseCAKeyPair(caCertPEM, caKeyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(caCertPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("failed to parse CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA cert: %w", err)
	}

	block, _ = pem.Decode(caKeyPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("failed to parse CA key PEM")
	}
	caKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA key: %w", err)
	}
	return caCert, caKey, nil
}

// ============================================================================
// File & Certificate Utilities
// ============================================================================

func loadCAFiles(certPath, keyPath string) ([]byte, []byte, error) {
	if !fileExists(certPath) || !fileExists(keyPath) {
		return nil, nil, fmt.Errorf(
			"CA not found at %s / %s — run 'gen-ca' first",
			certPath, keyPath,
		)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read CA cert: %w", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read CA key: %w", err)
	}
	return certPEM, keyPEM, nil
}

const certPairPrevSuffix = ".prev"

// publishCertKeyPair writes cert+key as one logical publish: snapshot any
// existing pair to <path>.prev, then write both; on failure restore the pair
// (or remove half-written files when there was no previous pair). Callers MUST
// call CommitCertPair after the DB binding succeeds, or RollbackCertPair when a
// later step fails, so the snapshots do not outlive the issuance transaction.
func certPairPrevPath(path string) string {
	return filepath.Join(filepath.Dir(path), fmt.Sprintf("%s%s", filepath.Base(path), certPairPrevSuffix))
}

func publishCertKeyPair(certPath, keyPath string, certPEM, keyPEM []byte) error {
	certPrev := certPairPrevPath(certPath)
	keyPrev := certPairPrevPath(keyPath)

	if err := snapshotToPrev(certPath, certPrev, false); err != nil {
		return fmt.Errorf("backup cert: %w", err)
	}
	if err := snapshotToPrev(keyPath, keyPrev, true); err != nil {
		return fmt.Errorf("backup key: %w", err)
	}

	if err := fsutil.WriteSharedFile(certPath, certPEM); err != nil {
		_ = restoreFromPrev(certPath, certPrev, false)
		_ = restoreFromPrev(keyPath, keyPrev, true)
		return fmt.Errorf("failed to write cert: %w", err)
	}
	if err := fsutil.WritePrivateFile(keyPath, keyPEM); err != nil {
		_ = restoreFromPrev(certPath, certPrev, false)
		_ = restoreFromPrev(keyPath, keyPrev, true)
		return fmt.Errorf("failed to write key: %w", err)
	}
	return nil
}

func snapshotToPrev(path, prev string, private bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			_ = os.Remove(prev)
			return nil
		}
		return err
	}
	if private {
		return fsutil.WritePrivateFile(prev, data)
	}
	return fsutil.WriteSharedFile(prev, data)
}

func restoreFromPrev(path, prev string, private bool) error {
	data, err := os.ReadFile(prev)
	if err != nil {
		if os.IsNotExist(err) {
			_ = os.Remove(path)
			return nil
		}
		return err
	}
	if private {
		return fsutil.WritePrivateFile(path, data)
	}
	return fsutil.WriteSharedFile(path, data)
}

// CommitCertPair removes the rollback snapshots after the caller has durably
// persisted the new binding (DB update) and no longer needs to roll back files.
func CommitCertPair(certPath, keyPath string) {
	_ = os.Remove(certPairPrevPath(certPath))
	_ = os.Remove(certPairPrevPath(keyPath))
}

// RollbackCertPair restores the pre-issuance cert/key pair from .prev and then
// removes the snapshots. It is used when the new pair was published but the
// subsequent DB binding failed, so files and DB stay consistent (old pair).
// If no snapshot exists (first issuance), the published pair is removed.
func RollbackCertPair(certPath, keyPath string) error {
	if err := restoreFromPrev(certPath, certPairPrevPath(certPath), false); err != nil {
		return err
	}
	if err := restoreFromPrev(keyPath, certPairPrevPath(keyPath), true); err != nil {
		return err
	}
	CommitCertPair(certPath, keyPath)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func parseCert(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("invalid certificate PEM")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}
	return parsed, nil
}

func fingerprintSHA256(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	return strings.ToLower(hex.EncodeToString(sum[:]))
}
