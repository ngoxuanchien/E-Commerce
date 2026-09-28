// Package grpcx builds gRPC servers and clients with the project defaults:
// OpenTelemetry instrumentation, panic recovery, health service and a default
// client deadline.
package grpcx

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// DefaultTimeout applies to client calls whose context has no deadline.
const DefaultTimeout = 5 * time.Second

// NewServer returns a server with tracing, panic recovery and a registered
// health service (reported SERVING for the overall server).
func NewServer(opts ...grpc.ServerOption) (*grpc.Server, *health.Server) {
	base := []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(recoverUnary),
		grpc.ChainStreamInterceptor(recoverStream),
	}
	srv := grpc.NewServer(append(base, opts...)...)
	h := health.NewServer()
	healthpb.RegisterHealthServer(srv, h)
	return srv, h
}

// Dial creates a plaintext client connection (internal network) with tracing
// and DefaultTimeout.
func Dial(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	base := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithChainUnaryInterceptor(DefaultTimeoutInterceptor(DefaultTimeout)),
	}
	return grpc.NewClient(target, append(base, opts...)...)
}

// DefaultTimeoutInterceptor sets a deadline of d on calls that have none.
func DefaultTimeoutInterceptor(d time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func recoverUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	defer func() {
		if v := recover(); v != nil {
			slog.ErrorContext(ctx, "grpc panic recovered", "method", info.FullMethod, "panic", v, "stack", string(debug.Stack()))
			err = status.Error(codes.Internal, "internal error")
		}
	}()
	return handler(ctx, req)
}

func recoverStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if v := recover(); v != nil {
			slog.ErrorContext(ss.Context(), "grpc panic recovered", "method", info.FullMethod, "panic", v, "stack", string(debug.Stack()))
			err = status.Error(codes.Internal, "internal error")
		}
	}()
	return handler(srv, ss)
}
