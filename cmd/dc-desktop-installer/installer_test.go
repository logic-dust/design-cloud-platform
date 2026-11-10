package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func generateTestCA(t *testing.T) ([]byte, *x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	serial, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Test Unit CA",
			Organization: []string{"Design Cloud Unit Test"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}

	return pemBytes, parsed, priv
}

func TestSaveClientConfigAndCert(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("APPDATA", tmpDir)

	caPEM, _, _ := generateTestCA(t)
	serverURL := "https://dc.cloud.hpc:1234"

	savedDir, err := SaveClientConfigAndCert(caPEM, serverURL)
	if err != nil {
		t.Fatalf("SaveClientConfigAndCert failed: %v", err)
	}

	expectedCert := filepath.Join(savedDir, "certs", "portal.ca.crt")
	certData, err := os.ReadFile(expectedCert)
	if err != nil {
		t.Fatalf("read saved ca cert: %v", err)
	}
	if string(certData) != string(caPEM) {
		t.Errorf("saved cert data mismatch")
	}

	expectedConfig := filepath.Join(savedDir, "config.json")
	cfgData, err := os.ReadFile(expectedConfig)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}

	var parsedCfg ClientAppConfig
	if err := json.Unmarshal(cfgData, &parsedCfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if parsedCfg.ServerURL != serverURL {
		t.Errorf("serverURL mismatch: got %q, want %q", parsedCfg.ServerURL, serverURL)
	}
	if parsedCfg.CACertFile != filepath.Join("certs", "portal.ca.crt") {
		t.Errorf("caCertFile mismatch: got %q", parsedCfg.CACertFile)
	}
}

func TestCreateSecureHTTPClient_AndDownload(t *testing.T) {
	caPEM, caCert, caPriv := generateTestCA(t)

	// Create test server TLS cert signed by test CA
	serverPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("server key: %v", err)
	}
	serverSerial, _ := rand.Int(rand.Reader, big.NewInt(2000000))
	serverTemplate := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject: pkix.Name{
			CommonName: "127.0.0.1",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverPriv.PublicKey, caPriv)
	if err != nil {
		t.Fatalf("create server cert: %v", err)
	}

	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	serverKeyDER, _ := x509.MarshalECPrivateKey(serverPriv)
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyDER})

	body := []byte("MOCK_SETUP_BINARY!")
	sum := sha256.Sum256(body)
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/desktop/releases/installer":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"fileName":"DesignCloudDesktop-win-Setup.exe","sha256":"` + hex.EncodeToString(sum[:]) + `","size":` + strconv.Itoa(len(body)) + `}}`))
		case "/api/desktop/releases/download/DesignCloudDesktop-win-Setup.exe":
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))

	tlsCert, err := tlsLoadX509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("load test server cert: %v", err)
	}
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}
	ts.StartTLS()
	defer ts.Close()

	// 1. Create client directly trusting test CA
	client, err := CreateSecureHTTPClient(caPEM)
	if err != nil {
		t.Fatalf("CreateSecureHTTPClient failed: %v", err)
	}

	destDir := t.TempDir()
	downloadedFile, err := DownloadPackage(client, ts.URL, "DesignCloudDesktop-win-Setup.exe", destDir, true)
	if err != nil {
		t.Fatalf("DownloadPackage failed: %v", err)
	}

	content, err := os.ReadFile(downloadedFile)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(content) != "MOCK_SETUP_BINARY!" {
		t.Errorf("content mismatch: got %q", string(content))
	}

	asset, err := FetchLatestWindowsAsset(client, ts.URL)
	if err != nil {
		t.Fatalf("FetchLatestWindowsAsset failed: %v", err)
	}
	if asset.FileName != "DesignCloudDesktop-win-Setup.exe" {
		t.Errorf("filename = %q", asset.FileName)
	}
	if err := verifyFileSHA256(downloadedFile, asset.SHA256); err != nil {
		t.Fatalf("verifyFileSHA256: %v", err)
	}
}

func TestFetchLatestWindowsAsset_RejectsInvalidInstallerName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/desktop/releases/installer", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"fileName":"../evil.exe","sha256":"abc","size":1}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	if _, err := FetchLatestWindowsAsset(ts.Client(), ts.URL); err == nil {
		t.Fatal("expected invalid installer name to be rejected")
	}
}

func tlsLoadX509KeyPair(certPEM, keyPEM []byte) (tls.Certificate, error) {
	return tls.X509KeyPair(certPEM, keyPEM)
}
