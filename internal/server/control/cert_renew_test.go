package control

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestCA creates a throwaway CA and leaf certificate pair for reload tests.
func writeTestCA(t *testing.T, dir string) (caCertPath, caKeyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	caCertPath = filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	caKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return caCertPath, caKeyPEM
}

func writeTestLeaf(t *testing.T, dir, name, caCertPath, caKeyPEM string) (certPath, keyPath string) {
	t.Helper()
	caPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	block, _ := pem.Decode(caPEM)
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	caBlock, _ := pem.Decode([]byte(caKeyPEM))
	caKey, err := x509.ParseECPrivateKey(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CA key: %v", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	certPath = filepath.Join(dir, name+".crt")
	keyPath = filepath.Join(dir, name+".key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatalf("write leaf cert: %v", err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatalf("write leaf key: %v", err)
	}
	return certPath, keyPath
}

// TestControlNodeCertHotReload 验证监听侧证书热重载：内容变化即换（无需重启），
// 且同 mtime 的内容变化也必须被内容哈希检测到（NFS 时间粒度不可靠）。
func TestControlNodeCertHotReload(t *testing.T) {
	dir := t.TempDir()
	caCertPath, caKeyPEM := writeTestCA(t, dir)
	certPath, keyPath := writeTestLeaf(t, dir, "node1", caCertPath, caKeyPEM)

	reloader := newCertReloader(certPath, keyPath)
	first, err := reloader.load()
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	again, err := reloader.load()
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if first != again {
		t.Fatal("unchanged certificate must reuse the cached entry")
	}

	// Record the original mtime, then replace the certificate with a NEW one while
	// forcibly preserving mtime: the content hash must still detect the swap.
	info, err := os.Stat(certPath)
	if err != nil {
		t.Fatalf("stat cert: %v", err)
	}
	oldMtime := info.ModTime()

	dir2 := t.TempDir()
	newCertPath, newKeyPath := writeTestLeaf(t, dir2, "node1", caCertPath, caKeyPEM)
	newCert, err := os.ReadFile(newCertPath)
	if err != nil {
		t.Fatalf("read new cert: %v", err)
	}
	newKey, err := os.ReadFile(newKeyPath)
	if err != nil {
		t.Fatalf("read new key: %v", err)
	}
	if err := os.WriteFile(certPath, newCert, 0644); err != nil {
		t.Fatalf("replace cert: %v", err)
	}
	if err := os.WriteFile(keyPath, newKey, 0600); err != nil {
		t.Fatalf("replace key: %v", err)
	}
	if err := os.Chtimes(certPath, oldMtime, oldMtime); err != nil {
		t.Fatalf("restore mtime: %v", err)
	}

	third, err := reloader.load()
	if err != nil {
		t.Fatalf("reload after swap: %v", err)
	}
	if third == first {
		t.Fatal("same-mtime content change must still trigger a reload (hash-primary)")
	}
}

func TestCertAlertStage(t *testing.T) {
	cases := []struct {
		remaining time.Duration
		want      int16
	}{
		{remaining: -time.Hour, want: certExpiredStage},
		{remaining: 3 * 24 * time.Hour, want: 7},
		{remaining: 20 * 24 * time.Hour, want: 30},
		{remaining: 60 * 24 * time.Hour, want: 90},
		{remaining: 200 * 24 * time.Hour, want: 0},
	}
	for _, tc := range cases {
		if got := certAlertStage(tc.remaining); got != tc.want {
			t.Fatalf("certAlertStage(%v) = %d, want %d", tc.remaining, got, tc.want)
		}
	}
}

// 配对性校验：证书与私钥必须属于同一密钥对；调换节点资产必须被检出。
func TestVerifyKeyPairing(t *testing.T) {
	dir := t.TempDir()
	caCertPath, caKeyPEM := writeTestCA(t, dir)
	_, otherCaKeyPEM := writeTestCA(t, t.TempDir())

	certPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	if err := verifyKeyPairing(cert, []byte(caKeyPEM)); err != nil {
		t.Fatalf("matching pair must pass: %v", err)
	}
	if err := verifyKeyPairing(cert, []byte(otherCaKeyPEM)); err == nil {
		t.Fatal("swapped key must be detected as a pairing error")
	}
	if err := verifyKeyPairing(cert, []byte("not-pem")); err == nil {
		t.Fatal("invalid PEM must be rejected")
	}
}
