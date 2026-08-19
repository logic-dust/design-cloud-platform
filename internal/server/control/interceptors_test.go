package control

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRecoveryUnaryServerInterceptor_Normal(t *testing.T) {
	interceptor := recoveryUnaryServerInterceptor(slog.Default())

	info := &grpc.UnaryServerInfo{FullMethod: "/control.ControlService/Test"}
	handler := func(ctx context.Context, req any) (any, error) {
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
	handler := func(ctx context.Context, req any) (any, error) {
		panic("simulated unary crash")
	}

	resp, err := interceptor(context.Background(), "ping", info, handler)
	if resp != nil {
		t.Fatalf("expected nil resp, got: %v", resp)
	}
	if err == nil {
		t.Fatal("expected error from panic, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Internal {
		t.Fatalf("expected codes.Internal, got: %v", err)
	}

	logStr := buf.String()
	if !strings.Contains(logStr, "gRPC unary handler panic recovered") {
		t.Fatalf("expected panic log message, got: %s", logStr)
	}
	if !strings.Contains(logStr, "simulated unary crash") {
		t.Fatalf("expected panic detail in log, got: %s", logStr)
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
	handler := func(srv any, stream grpc.ServerStream) error {
		return nil
	}

	err := interceptor(nil, &dummyServerStream{}, info, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecoveryStreamServerInterceptor_Panic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	interceptor := recoveryStreamServerInterceptor(logger)

	info := &grpc.StreamServerInfo{FullMethod: "/control.ControlService/Sync"}
	handler := func(srv any, stream grpc.ServerStream) error {
		panic("simulated stream crash")
	}

	err := interceptor(nil, &dummyServerStream{}, info, handler)
	if err == nil {
		t.Fatal("expected error from panic, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Internal {
		t.Fatalf("expected codes.Internal, got: %v", err)
	}

	logStr := buf.String()
	if !strings.Contains(logStr, "gRPC stream handler panic recovered") {
		t.Fatalf("expected panic log message, got: %s", logStr)
	}
	if !strings.Contains(logStr, "simulated stream crash") {
		t.Fatalf("expected panic detail in log, got: %s", logStr)
	}
}
