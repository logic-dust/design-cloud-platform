package controlplane

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"design-cloud-engine/internal/contract"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	defaultKeepaliveTime    = 30 * time.Second
	defaultKeepaliveTimeout = 5 * time.Second
	defaultKeepaliveMinTime = 10 * time.Second
)

// Certs holds the paths to the mTLS certificates and keys.
type Certs struct {
	CertPath string // Path to the node certificate
	KeyPath  string // Path to the node private key
	CAPath   string // Path to the CA certificate
}

func newGRPCServer(certs Certs, logger *slog.Logger) (*grpc.Server, error) {
	creds, err := buildServerTLSCredentials(certs)
	if err != nil {
		return nil, err
	}
	return grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    defaultKeepaliveTime,
			Timeout: defaultKeepaliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             defaultKeepaliveMinTime,
			PermitWithoutStream: true,
		}),
		grpc.Creds(creds),
		grpc.ChainUnaryInterceptor(
			recoveryUnaryServerInterceptor(logger),
			domainErrorUnaryServerInterceptor(),
		),
		grpc.ChainStreamInterceptor(recoveryStreamServerInterceptor(logger)),
	), nil
}

// certReloader serves the listener/client certificate from disk with content-hash
// based reload detection. Content is authoritative: mtime is deliberately NOT used
// as a skip condition, because NFS timestamp granularity can mask same-second
// writes and would silently keep a stale certificate (D12).
type certReloader struct {
	certPath string
	keyPath  string

	mu       sync.Mutex
	cert     *tls.Certificate
	certHash [sha256.Size]byte
	keyHash  [sha256.Size]byte
}

func newCertReloader(certPath, keyPath string) *certReloader {
	return &certReloader{certPath: certPath, keyPath: keyPath}
}

// load returns the current certificate, reloading from disk when the content changed.
func (r *certReloader) load() (*tls.Certificate, error) {
	certPEM, err := os.ReadFile(r.certPath)
	if err != nil {
		return nil, fmt.Errorf("read certificate %s: %w", r.certPath, err)
	}
	keyPEM, err := os.ReadFile(r.keyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key %s: %w", r.keyPath, err)
	}
	certHash := sha256.Sum256(certPEM)
	keyHash := sha256.Sum256(keyPEM)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cert != nil && certHash == r.certHash && keyHash == r.keyHash {
		return r.cert, nil
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse key pair: %w", err)
	}
	r.cert = &cert
	r.certHash = certHash
	r.keyHash = keyHash
	return r.cert, nil
}

// GetCertificate is a tls.Config callback: the listener uses the certificate
// currently on disk, so renewal takes effect on the next handshake without restart.
func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return r.load()
}

// GetClientCertificate is the outbound counterpart for control-to-control calls.
func (r *certReloader) GetClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return r.load()
}

func loadTLSCredentials(certs Certs) (*x509.CertPool, error) {
	// Load once eagerly so a broken certificate fails at startup, then hand the
	// reloader to the TLS config for hot reload.
	reloader := newCertReloader(certs.CertPath, certs.KeyPath)
	if _, err := reloader.load(); err != nil {
		return nil, fmt.Errorf("failed to load key pair: %w", err)
	}

	pool := x509.NewCertPool()
	ca, err := os.ReadFile(certs.CAPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate from %s: %w", certs.CAPath, err)
	}
	if ok := pool.AppendCertsFromPEM(ca); !ok {
		return nil, fmt.Errorf("failed to append CA certificate")
	}

	return pool, nil
}

// buildServerTLSCredentials builds TLS credentials for the server.
func buildServerTLSCredentials(certs Certs) (credentials.TransportCredentials, error) {
	pool, err := loadTLSCredentials(certs)
	if err != nil {
		return nil, err
	}
	reloader := newCertReloader(certs.CertPath, certs.KeyPath)
	return credentials.NewTLS(&tls.Config{
		GetCertificate: reloader.GetCertificate,
		ClientAuth:     tls.RequireAndVerifyClientCert,
		ClientCAs:      pool,
		MinVersion:     tls.VersionTLS13,
	}), nil
}

// LocalCertFingerprint computes the SHA256 fingerprint of the node's own
// certificate file on disk. Control nodes use this to self-register their
// fingerprint via UpsertSelf so their local agent can pass gRPC identity
// validation without the OTP bootstrap flow. The fingerprint format matches
// the peer-cert path (extractPeerCertInfo): sha256.Sum256 of the DER cert,
// lowercase hex.
func LocalCertFingerprint(certs Certs) (string, error) {
	cert, err := tls.LoadX509KeyPair(certs.CertPath, certs.KeyPath)
	if err != nil {
		return "", fmt.Errorf("failed to load local key pair: %w", err)
	}
	if len(cert.Certificate) == 0 {
		return "", fmt.Errorf("local certificate is empty")
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return "", fmt.Errorf("failed to parse local certificate: %w", err)
	}
	sum := sha256.Sum256(parsed.Raw)
	return hex.EncodeToString(sum[:]), nil
}

// buildClientTLSCredentials builds mTLS credentials for control-to-control calls.
func buildClientTLSCredentials(certs Certs, serverName string) (credentials.TransportCredentials, error) {
	pool, err := loadTLSCredentials(certs)
	if err != nil {
		return nil, err
	}
	reloader := newCertReloader(certs.CertPath, certs.KeyPath)
	return credentials.NewTLS(&tls.Config{
		GetClientCertificate: reloader.GetClientCertificate,
		RootCAs:              pool,
		ServerName:           serverName,
		MinVersion:           tls.VersionTLS13,
	}), nil
}

// peerCertInfo is the set of peer certificate attributes the control plane records.
type peerCertInfo struct {
	Fingerprint string
	Subject     string
	NotAfter    time.Time
}

// extractPeerCertInfo extracts identity metadata from the peer TLS certificate.
func extractPeerCertInfo(ctx context.Context) (*peerCertInfo, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("no peer info in context")
	}

	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil, fmt.Errorf("no TLS info available")
	}

	if len(tlsInfo.State.PeerCertificates) == 0 {
		return nil, fmt.Errorf("no peer certificates")
	}

	cert := tlsInfo.State.PeerCertificates[0]
	fingerprint := sha256.Sum256(cert.Raw)
	return &peerCertInfo{
		Fingerprint: hex.EncodeToString(fingerprint[:]),
		Subject:     cert.Subject.String(),
		NotAfter:    cert.NotAfter,
	}, nil
}

func recoveryUnaryServerInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				if logger != nil {
					logger.Error("gRPC unary handler panic recovered",
						"method", info.FullMethod,
						"panic", r,
						"stack", string(debug.Stack()),
					)
				}
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

func recoveryStreamServerInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				if logger != nil {
					logger.Error("gRPC stream handler panic recovered",
						"method", info.FullMethod,
						"panic", r,
						"stack", string(debug.Stack()),
					)
				}
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		return handler(srv, stream)
	}
}

func domainErrorUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		resp, err = handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if domainErr, ok := errors.AsType[contract.DomainError](err); ok {
			return nil, status.Error(domainErr.GRPCCode(), domainErr.Message())
		}
		return resp, err
	}
}
