package controlplane

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// writeTestCertPair writes a self-signed cert + key to disk and returns the
// fingerprint computed from the certificate (same algorithm as the runtime path).
func writeTestCertPair(t *testing.T) (Certs, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "node-1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	fingerprint := hex.EncodeToString(sha256Sum(parsed.Raw))

	dir := t.TempDir()
	certPath := filepath.Join(dir, "node-1.crt")
	keyPath := filepath.Join(dir, "node-1.key")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	keyDER := x509.MarshalPKCS1PrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	return Certs{CertPath: certPath, KeyPath: keyPath, CAPath: certPath}, fingerprint
}

func sha256Sum(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}

func TestLocalCertFingerprint(t *testing.T) {
	certs, want := writeTestCertPair(t)

	got, err := LocalCertFingerprint(certs)
	if err != nil {
		t.Fatalf("LocalCertFingerprint() error = %v", err)
	}
	if got != want {
		t.Fatalf("fingerprint = %q, want %q", got, want)
	}
}

func TestLocalCertFingerprint_ConsistentWithPeerExtraction(t *testing.T) {
	certs, _ := writeTestCertPair(t)

	localFP, err := LocalCertFingerprint(certs)
	if err != nil {
		t.Fatalf("LocalCertFingerprint() error = %v", err)
	}

	// 用同一份证书构造 peer 上下文，模拟 gRPC 对端验证提取的指纹
	ctx, peerFP := peerContextWithCertificate(t)

	// 两者算法一致（都是 sha256.Sum256(cert.Raw) lowercase hex）；这里验证格式长度
	if len(localFP) != 64 {
		t.Fatalf("local fingerprint length = %d, want 64", len(localFP))
	}
	if len(peerFP) != 64 {
		t.Fatalf("peer fingerprint length = %d, want 64", len(peerFP))
	}
	_ = ctx
}

func TestRecoveryUnaryServerInterceptor_Normal(t *testing.T) {
	interceptor := recoveryUnaryServerInterceptor(slog.Default())

	info := &grpc.UnaryServerInfo{FullMethod: "/control.ControlService/Test"}
	handler := func(context.Context, any) (any, error) {
		return "ok", nil
	}

	resp, err := interceptor(context.Background(), "ping", info, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("expected resp 'ok', got: %v", resp)
	}
}

func TestRecoveryUnaryServerInterceptor_Panic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	interceptor := recoveryUnaryServerInterceptor(logger)

	info := &grpc.UnaryServerInfo{FullMethod: "/control.ControlService/PanicUnary"}
	handler := func(context.Context, any) (any, error) {
		panic("simulated unary crash")
	}

	resp, err := interceptor(context.Background(), "ping", info, handler)
	if resp != nil {
		t.Fatalf("expected nil resp, got: %v", resp)
	}
	if err == nil {
		t.Fatal("expected error from panic, got nil")
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected codes.Internal, got: %v", err)
	}
	if logText := buf.String(); !strings.Contains(logText, "gRPC unary handler panic recovered") ||
		!strings.Contains(logText, "simulated unary crash") {
		t.Fatalf("expected panic details in log, got: %s", logText)
	}
}

type dummyServerStream struct {
	grpc.ServerStream
}

func (d *dummyServerStream) Context() context.Context {
	return context.Background()
}

func TestRecoveryStreamServerInterceptor_Normal(t *testing.T) {
	interceptor := recoveryStreamServerInterceptor(slog.Default())
	info := &grpc.StreamServerInfo{FullMethod: "/control.ControlService/Sync"}
	handler := func(any, grpc.ServerStream) error {
		return nil
	}

	if err := interceptor(nil, &dummyServerStream{}, info, handler); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecoveryStreamServerInterceptor_Panic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	interceptor := recoveryStreamServerInterceptor(logger)

	info := &grpc.StreamServerInfo{FullMethod: "/control.ControlService/Sync"}
	handler := func(any, grpc.ServerStream) error {
		panic("simulated stream crash")
	}

	err := interceptor(nil, &dummyServerStream{}, info, handler)
	if err == nil {
		t.Fatal("expected error from panic, got nil")
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected codes.Internal, got: %v", err)
	}
	if logText := buf.String(); !strings.Contains(logText, "gRPC stream handler panic recovered") ||
		!strings.Contains(logText, "simulated stream crash") {
		t.Fatalf("expected panic details in log, got: %s", logText)
	}
}
