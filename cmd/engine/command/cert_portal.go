package command

import (
	"cmp"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"design-cloud-engine/cmd/engine/console"
	"design-cloud-engine/internal/platform/config"
	fsutil "design-cloud-engine/internal/platform/fs"
	"design-cloud-engine/internal/platform/overlay"
)

// PortalCertOptions contains options for generating portal TLS certificates.
type PortalCertOptions struct {
	Hosts        []string
	OutDir       string
	Days         int
	CADays       int
	CAPrefix     string
	SignedPrefix string
	UseRSA       bool
	Force        bool
}

// PortalCertResult contains the generated portal TLS certificate paths and fingerprints.
type PortalCertResult struct {
	CACertPath        string
	CAKeyPath         string
	ServerCertPath    string
	ServerKeyPath     string
	FullchainPath     string
	CAFingerprint     string
	ServerFingerprint string
}

func runCertCommand(flags *Flags, args []string) error {
	if len(args) == 0 {
		printCertUsage()
		return fmt.Errorf("cert sub-command required: gen-portal, inject-installer")
	}
	switch args[0] {
	case "gen-portal":
		return runGenPortalCertCommand(args[1:])
	case "inject-installer":
		return runInjectInstallerCommand(args[1:])
	default:
		printCertUsage()
		return fmt.Errorf("unknown cert command: %s", args[0])
	}
}

func printCertUsage() {
	console.PrintStderr("Usage: %s cert <gen-portal|inject-installer> [options]\n\n", config.AppName)
	console.PrintStderr("Subcommands:\n")
	console.PrintStderr("  gen-portal               Generate self-signed CA and server TLS certificates for Portal\n")
	console.PrintStderr("  inject-installer         Inject CA certificate & server URL into desktop installer EXE\n")
}

func runInjectInstallerCommand(args []string) error {
	fs := newCommandFlagSet("inject-installer", os.Stderr)

	var caPath, inputPath, outputPath, serverURL string
	var force bool

	fs.StringVar(&caPath, "ca", "conf/certs/portal/portal.ca.crt", "Path to CA certificate file")
	fs.StringVar(&inputPath, "input", "", "Path to template installer EXE (required)")
	fs.StringVar(&outputPath, "output", "build/DesignCloudDesktop-Installer.exe", "Path to output installer EXE")
	fs.StringVar(&serverURL, "server-url", "", "Target Portal Server URL (optional, e.g. https://dc.cloud.hpc:1234)")
	fs.BoolVar(&force, "force", false, "Force overwrite output file if it exists")

	fs.Usage = func() {
		console.PrintStderr("Usage: %s cert inject-installer --input <template.exe> [options]\n\n", config.AppName)
		console.PrintStderr("Inject a CA certificate and server URL into a Windows desktop installer template.\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("cert inject-installer parse error: %w", err)
	}

	if inputPath == "" {
		return fmt.Errorf("--input template executable is required")
	}

	if !force {
		if _, err := os.Stat(outputPath); err == nil {
			return fmt.Errorf("refusing to overwrite existing file %q (use --force to overwrite)", outputPath)
		}
	}

	caData, err := os.ReadFile(caPath)
	if err != nil {
		return fmt.Errorf("read CA certificate %q: %w", caPath, err)
	}

	block, _ := pem.Decode(caData)
	if block == nil || block.Type != "CERTIFICATE" {
		return fmt.Errorf("invalid CA file %q: no PEM certificate found", caPath)
	}

	parsedCA, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse CA certificate: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	payload := &overlay.Payload{
		CACertPEM: string(caData),
		ServerURL: serverURL,
	}

	if err := overlay.Inject(inputPath, outputPath, payload); err != nil {
		return fmt.Errorf("inject overlay payload: %w", err)
	}

	console.PrintStdout("Successfully injected CA certificate into installer:\n")
	console.PrintStdout("  Input template  : %s\n", inputPath)
	console.PrintStdout("  Output installer: %s\n", outputPath)
	console.PrintStdout("  CA Subject      : %s\n", parsedCA.Subject.CommonName)
	console.PrintStdout("  CA Fingerprint  : %s\n", formatSHA256Fingerprint(parsedCA))
	if serverURL != "" {
		console.PrintStdout("  Server URL      : %s\n", serverURL)
	}
	console.PrintStdout("\nDone.\n")
	return nil
}

func runGenPortalCertCommand(args []string) error {
	fs := newCommandFlagSet("gen-portal", os.Stderr)

	var hostsFlag, outDir, caPrefix, signedPrefix string
	var days, caDays int
	var useRSA, force bool

	fs.StringVar(&hostsFlag, "hosts", "localhost,127.0.0.1", "Comma-separated DNS/IP SAN list")
	fs.StringVar(&outDir, "out-dir", "conf/certs/portal", "Output directory for certificates")
	fs.IntVar(&days, "days", 1095, "Server certificate validity in days (default: 3 years)")
	fs.IntVar(&caDays, "ca-days", 3650, "CA certificate validity in days (default: 10 years)")
	fs.StringVar(&caPrefix, "ca-prefix", "portal.ca", "CA file prefix (creates <prefix>.crt and <prefix>.key)")
	fs.StringVar(&signedPrefix, "signed-prefix", "portal", "Server cert/key prefix (creates <prefix>.crt and <prefix>.key)")
	fs.BoolVar(&useRSA, "rsa", false, "Generate RSA 2048 keys instead of ECDSA P-256")
	fs.BoolVar(&force, "force", false, "Force overwrite existing certificate files")

	fs.Usage = func() {
		console.PrintStderr("Usage: %s cert gen-portal [options]\n\n", config.AppName)
		console.PrintStderr("Generate self-signed CA and server TLS certificates for Portal HTTPS.\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("cert gen-portal parse error: %w", err)
	}

	rawHosts := strings.Split(hostsFlag, ",")
	var hosts []string
	for _, h := range rawHosts {
		h = strings.TrimSpace(h)
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) == 0 {
		return fmt.Errorf("--hosts cannot be empty")
	}

	opts := PortalCertOptions{
		Hosts:        hosts,
		OutDir:       outDir,
		Days:         days,
		CADays:       caDays,
		CAPrefix:     caPrefix,
		SignedPrefix: signedPrefix,
		UseRSA:       useRSA,
		Force:        force,
	}

	res, err := GeneratePortalCertificates(opts)
	if err != nil {
		return err
	}

	adjustCertOwnershipIfRoot(res, outDir)

	console.PrintStdout("Generated portal TLS files in: %s\n", outDir)
	console.PrintStdout("Hosts: %s\n\n", strings.Join(hosts, ", "))
	console.PrintStdout("Done:\n")
	console.PrintStdout("  CA cert    : %s\n", res.CACertPath)
	console.PrintStdout("  CA key     : %s\n", res.CAKeyPath)
	console.PrintStdout("  Server cert: %s\n", res.ServerCertPath)
	console.PrintStdout("  Server key : %s\n", res.ServerKeyPath)
	console.PrintStdout("  Full chain : %s\n\n", res.FullchainPath)
	console.PrintStdout("Fingerprints:\n")
	console.PrintStdout("  CA SHA-256    : %s\n", res.CAFingerprint)
	console.PrintStdout("  Server SHA-256: %s\n\n", res.ServerFingerprint)
	console.PrintStdout("Use these paths with portal.cert / portal.key when portal.tls=true.\n")
	return nil
}

// GeneratePortalCertificates creates a self-signed CA and an issued server certificate for Portal HTTPS.
func GeneratePortalCertificates(opts PortalCertOptions) (*PortalCertResult, error) {
	if len(opts.Hosts) == 0 {
		return nil, fmt.Errorf("hosts list cannot be empty")
	}
	if opts.Days <= 0 {
		opts.Days = 1095
	}
	if opts.CADays <= 0 {
		opts.CADays = 3650
	}
	caPrefix := filepath.Base(cmp.Or(opts.CAPrefix, "portal.ca"))
	signedPrefix := filepath.Base(cmp.Or(opts.SignedPrefix, "portal"))
	outDir := filepath.Clean(cmp.Or(opts.OutDir, "conf/certs/portal"))

	caCertName := fmt.Sprintf("%s.crt", caPrefix)
	caKeyName := fmt.Sprintf("%s.key", caPrefix)
	serverCertName := fmt.Sprintf("%s.crt", signedPrefix)
	serverKeyName := fmt.Sprintf("%s.key", signedPrefix)
	fullchainName := fmt.Sprintf("%s.fullchain.crt", signedPrefix)

	caCertPath := filepath.Join(outDir, caCertName)
	caKeyPath := filepath.Join(outDir, caKeyName)
	serverCertPath := filepath.Join(outDir, serverCertName)
	serverKeyPath := filepath.Join(outDir, serverKeyName)
	fullchainPath := filepath.Join(outDir, fullchainName)

	if !opts.Force {
		files := []string{caCertPath, caKeyPath, serverCertPath, serverKeyPath, fullchainPath}
		for _, f := range files {
			if _, err := os.Stat(f); err == nil {
				return nil, fmt.Errorf("refusing to overwrite existing file %q (use --force to overwrite)", f)
			}
		}
	}

	if err := fsutil.MkdirPrivate(outDir); err != nil {
		return nil, fmt.Errorf("create certs directory %q: %w", outDir, err)
	}

	// 1. Generate CA key and certificate
	caPriv, caPubKey, caKeyPEM, err := generateKeyPair(opts.UseRSA)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	caSerial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, fmt.Errorf("generate CA serial: %w", err)
	}

	now := time.Now().UTC()
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial,
		Subject: pkix.Name{
			Organization:       []string{"Design Cloud Engine"},
			OrganizationalUnit: []string{"Web"},
			CommonName:         "Design Cloud Engine CA",
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(time.Duration(opts.CADays) * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPubKey, caPriv)
	if err != nil {
		return nil, fmt.Errorf("create CA certificate: %w", err)
	}
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})

	// 2. Generate Server key and certificate
	_, serverPubKey, serverKeyPEM, err := generateKeyPair(opts.UseRSA)
	if err != nil {
		return nil, fmt.Errorf("generate server key: %w", err)
	}

	serverSerial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, fmt.Errorf("generate server serial: %w", err)
	}

	serverCommonName := opts.Hosts[0]
	serverTemplate := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject: pkix.Name{
			Organization:       []string{"Design Cloud Engine"},
			OrganizationalUnit: []string{"Web"},
			CommonName:         serverCommonName,
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(time.Duration(opts.Days) * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	for _, h := range opts.Hosts {
		if ip := net.ParseIP(h); ip != nil {
			serverTemplate.IPAddresses = append(serverTemplate.IPAddresses, ip)
		} else {
			serverTemplate.DNSNames = append(serverTemplate.DNSNames, h)
		}
	}

	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, serverPubKey, caPriv)
	if err != nil {
		return nil, fmt.Errorf("create server certificate: %w", err)
	}
	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCertDER})

	// 3. Prepare fullchain PEM
	fullchainPEM := append(serverCertPEM, caCertPEM...)

	// 4. Write files
	if err := fsutil.WritePrivateFile(caKeyPath, caKeyPEM); err != nil {
		return nil, fmt.Errorf("write CA key: %w", err)
	}
	if err := fsutil.WriteSharedFile(caCertPath, caCertPEM); err != nil {
		return nil, fmt.Errorf("write CA cert: %w", err)
	}
	if err := fsutil.WritePrivateFile(serverKeyPath, serverKeyPEM); err != nil {
		return nil, fmt.Errorf("write server key: %w", err)
	}
	if err := fsutil.WriteSharedFile(serverCertPath, serverCertPEM); err != nil {
		return nil, fmt.Errorf("write server cert: %w", err)
	}
	if err := fsutil.WriteSharedFile(fullchainPath, fullchainPEM); err != nil {
		return nil, fmt.Errorf("write fullchain cert: %w", err)
	}

	caParsed, _ := x509.ParseCertificate(caCertDER)
	serverParsed, _ := x509.ParseCertificate(serverCertDER)

	return &PortalCertResult{
		CACertPath:        caCertPath,
		CAKeyPath:         caKeyPath,
		ServerCertPath:    serverCertPath,
		ServerKeyPath:     serverKeyPath,
		FullchainPath:     fullchainPath,
		CAFingerprint:     formatSHA256Fingerprint(caParsed),
		ServerFingerprint: formatSHA256Fingerprint(serverParsed),
	}, nil
}

func generateKeyPair(useRSA bool) (crypto.PrivateKey, crypto.PublicKey, []byte, error) {
	if useRSA {
		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, nil, nil, err
		}
		der := x509.MarshalPKCS1PrivateKey(priv)
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
		return priv, &priv.PublicKey, pemBytes, nil
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	return priv, &priv.PublicKey, pemBytes, nil
}

func formatSHA256Fingerprint(cert *x509.Certificate) string {
	if cert == nil {
		return "unknown"
	}
	sum := sha256.Sum256(cert.Raw)
	encoded := hex.EncodeToString(sum[:])
	var sb strings.Builder
	for i := 0; i < len(encoded); i += 2 {
		if i > 0 {
			sb.WriteByte(':')
		}
		sb.WriteString(strings.ToUpper(encoded[i : i+2]))
	}
	return sb.String()
}
