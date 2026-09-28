package grpcx_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
)

func startBufconn(t *testing.T, opts ...grpc.ServerOption) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv, _ := grpcx.NewServer(opts...)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpcx.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestServerExposesHealth(t *testing.T) {
	conn := startBufconn(t)
	resp, err := healthpb.NewHealthClient(conn).Check(context.Background(), &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus())
}

func TestServerRecoversFromPanics(t *testing.T) {
	conn := startBufconn(t, grpc.UnknownServiceHandler(func(any, grpc.ServerStream) error {
		panic("boom")
	}))
	err := conn.Invoke(context.Background(), "/test.v1.Nope/Call", &emptypb.Empty{}, &emptypb.Empty{})
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestDefaultTimeoutInterceptorAddsDeadline(t *testing.T) {
	icpt := grpcx.DefaultTimeoutInterceptor(time.Second)
	var deadline time.Time
	var ok bool
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		deadline, ok = ctx.Deadline()
		return nil
	}

	require.NoError(t, icpt(context.Background(), "/m", nil, nil, nil, invoker))
	assert.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(time.Second), deadline, 200*time.Millisecond)
}

func TestDefaultTimeoutInterceptorKeepsCallerDeadline(t *testing.T) {
	icpt := grpcx.DefaultTimeoutInterceptor(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	want, _ := ctx.Deadline()

	var got time.Time
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		got, _ = ctx.Deadline()
		return nil
	}
	require.NoError(t, icpt(ctx, "/m", nil, nil, nil, invoker))
	assert.Equal(t, want, got)
}
