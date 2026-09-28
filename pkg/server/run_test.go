package server_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestRunServesAndShutsDownGracefully(t *testing.T) {
	httpAddr, grpcAddr := freeAddr(t), freeAddr(t)
	grpcSrv := grpc.NewServer()
	healthpb.RegisterHealthServer(grpcSrv, health.NewServer())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, server.Config{
			HTTP:            &http.Server{Addr: httpAddr, Handler: server.Healthz(), ReadHeaderTimeout: time.Second},
			GRPC:            grpcSrv,
			GRPCAddr:        grpcAddr,
			ShutdownTimeout: 2 * time.Second,
			Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}()

	require.Eventually(t, func() bool { return server.Probe("http://"+httpAddr) == nil }, 3*time.Second, 20*time.Millisecond)

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	resp, err := healthpb.NewHealthClient(conn).Check(context.Background(), &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus())

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	assert.Error(t, server.Probe("http://"+httpAddr))
}

func TestRunReturnsListenError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	err = server.Run(context.Background(), server.Config{
		HTTP:   &http.Server{Addr: l.Addr().String(), ReadHeaderTimeout: time.Second},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	assert.ErrorContains(t, err, "listen http")
}
