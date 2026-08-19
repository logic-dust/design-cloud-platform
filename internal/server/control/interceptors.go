package control

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"

	"design-cloud-engine/internal/common"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// recoveryUnaryServerInterceptor returns a new unary server interceptor that recovers from panics in unary RPCs.
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
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

// recoveryStreamServerInterceptor returns a new stream server interceptor that recovers from panics in streaming RPCs.
func recoveryStreamServerInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				if logger != nil {
					logger.Error("gRPC stream handler panic recovered",
						"method", info.FullMethod,
						"panic", r,
						"stack", string(debug.Stack()),
					)
				}
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()
		return handler(srv, ss)
	}
}

// domainErrorUnaryServerInterceptor converts internal DomainErrors to gRPC status errors.
func domainErrorUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		resp, err = handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if domainErr, ok := errors.AsType[common.DomainError](err); ok {
			return nil, status.Error(domainErr.GRPCCode(), domainErr.Message())
		}
		return resp, err
	}
}
