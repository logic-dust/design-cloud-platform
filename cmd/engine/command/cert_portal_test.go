package command

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"

	"design-cloud-engine/internal/platform/overlay"
)

func TestGeneratePortalCertificates_ECDSA(t *testing.T) {
	outDir := t.TempDir()

	opts := PortalCertOptions{
		Hosts:        []string{"localhost", "127.0.0.1", "dc.cloud.hpc", "10.0.0.10"},
		OutDir:       outDir,
		Days:         30,
		CADays:       60,
		CAPrefix:     "portal.ca",
		SignedPrefix: "portal",
		UseRSA:       false,
		Force:        false,
	}

	res, err := GeneratePortalCertificates(opts)
	if err != nil {
		t.Fatalf("GeneratePortalCertificates failed: %v", err)
	}

	if res.CACertPath != filepath.Join(outDir, "portal.ca.crt") {
		t.Errorf("unexpected CACertPath: %s", res.CACertPath)
	}
	if res.CAFingerprint == "" || res.CAFingerprint == "unknown" {
		t.Errorf("empty CA fingerprint")
	}
	if res.ServerFingerprint == "" || res.ServerFingerprint == "unknown" {
		t.Errorf("empty server fingerprint")
	}

	// 1. Verify TLS pair loading
	pair, err := tls.LoadX509KeyPair(res.ServerCertPath, res.ServerKeyPath)
	if err != nil {
		t.Fatalf("tls.LoadX509KeyPair failed: %v", err)
	}
	if len(pair.Certificate) == 0 {
		t.Fatalf("no certificate loaded in pair")
	}

	// 2. Verify CA cert and server cert with Go crypto/x509
	caPEM, err := os.ReadFile(res.CACertPath)
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	serverPEM, err := os.ReadFile(res.ServerCertPath)
	if err != nil {
		t.Fatalf("read server cert: %v", err)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatalf("failed to append CA cert to roots")
	}

	serverCert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("parse server cert: %v", err)
	}

	if len(serverCert.DNSNames) < 2 || len(serverCert.IPAddresses) < 2 {
		t.Errorf("SANs not populated correctly: DNS=%v, IP=%v", serverCert.DNSNames, serverCert.IPAddresses)
	}

	verifyOpts := x509.VerifyOptions{
		Roots:   roots,
		DNSName: "dc.cloud.hpc",
	}
	if _, err := serverCert.Verify(verifyOpts); err != nil {
		t.Errorf("server certificate failed verification against CA: %v", err)
	}

	// 3. Verify Fullchain
	fcPEM, err := os.ReadFile(res.FullchainPath)
	if err != nil {
		t.Fatalf("read fullchain cert: %v", err)
	}
	if len(fcPEM) <= len(serverPEM) {
		t.Errorf("fullchain should be longer than leaf server cert")
	}

	// 4. Verify force=false prevents overwrite
	_, err = GeneratePortalCertificates(opts)
	if err == nil {
		t.Errorf("expected error overwriting existing files when force=false, got nil")
	}

	// 5. Verify force=true succeeds
	opts.Force = true
	_, err = GeneratePortalCertificates(opts)
	if err != nil {
		t.Errorf("force=true should succeed, got: %v", err)
	}
}

func TestGeneratePortalCertificates_RSA(t *testing.T) {
	outDir := t.TempDir()

	opts := PortalCertOptions{
		Hosts:        []string{"localhost", "127.0.0.1"},
		OutDir:       outDir,
		Days:         30,
		CADays:       60,
		CAPrefix:     "test.ca",
		SignedPrefix: "test",
		UseRSA:       true,
		Force:        false,
	}

	res, err := GeneratePortalCertificates(opts)
	if err != nil {
		t.Fatalf("GeneratePortalCertificates (RSA) failed: %v", err)
	}

	pair, err := tls.LoadX509KeyPair(res.ServerCertPath, res.ServerKeyPath)
	if err != nil {
		t.Fatalf("tls.LoadX509KeyPair failed: %v", err)
	}
	if len(pair.Certificate) == 0 {
		t.Fatalf("no certificate loaded in pair")
	}
}

func TestRunCertCommand_CLI(t *testing.T) {
	outDir := t.TempDir()

	err := Execute(&Flags{}, []string{"cert", "gen-portal", "--out-dir", outDir, "--hosts", "127.0.0.1,localhost"})
	if err != nil {
		t.Fatalf("Execute(cert gen-portal) failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "portal.ca.crt")); err != nil {
		t.Errorf("expected portal.ca.crt to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "portal.crt")); err != nil {
		t.Errorf("expected portal.crt to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "portal.fullchain.crt")); err != nil {
		t.Errorf("expected portal.fullchain.crt to exist: %v", err)
	}
}

func TestRunCertInjectInstaller_CLI(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Generate certs first
	certDir := filepath.Join(tmpDir, "certs")
	if err := Execute(&Flags{}, []string{"cert", "gen-portal", "--out-dir", certDir}); err != nil {
		t.Fatalf("Execute(cert gen-portal) failed: %v", err)
	}

	caPath := filepath.Join(certDir, "portal.ca.crt")
	templateExe := filepath.Join(tmpDir, "template.exe")
	outputExe := filepath.Join(tmpDir, "installer.exe")

	if err := os.WriteFile(templateExe, []byte("MOCK_PE_BINARY_CONTENTS"), 0755); err != nil {
		t.Fatalf("write template exe: %v", err)
	}

	// 2. Run inject-installer
	args := []string{
		"cert", "inject-installer",
		"--ca", caPath,
		"--input", templateExe,
		"--output", outputExe,
		"--server-url", "https://dc.cloud.hpc:1234",
	}

	if err := Execute(&Flags{}, args); err != nil {
		t.Fatalf("Execute(cert inject-installer) failed: %v", err)
	}

	// 3. Verify output file exists and can be read by overlay
	if _, err := os.Stat(outputExe); err != nil {
		t.Fatalf("expected output exe to exist: %v", err)
	}

	p, err := overlay.ReadFile(outputExe)
	if err != nil {
		t.Fatalf("overlay.ReadFile failed: %v", err)
	}

	if p.ServerURL != "https://dc.cloud.hpc:1234" {
		t.Errorf("expected serverURL 'https://dc.cloud.hpc:1234', got %q", p.ServerURL)
	}
	if p.CACertPEM == "" {
		t.Errorf("expected non-empty CACertPEM")
	}
}
