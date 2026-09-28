package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"
)

// Config describes what Run serves. GRPC may be nil for HTTP-only services.
type Config struct {
	HTTP            *http.Server
	GRPC            *grpc.Server
	GRPCAddr        string
	ShutdownTimeout time.Duration
	Logger          *slog.Logger
}

// Run serves HTTP (and gRPC when configured) until ctx is cancelled or a
// server fails, then shuts both down gracefully within ShutdownTimeout.
func Run(ctx context.Context, cfg Config) error {
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 15 * time.Second
	}
	var lc net.ListenConfig
	httpLn, err := lc.Listen(ctx, "tcp", cfg.HTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen http %s: %w", cfg.HTTP.Addr, err)
	}
	errCh := make(chan error, 2)
	go func() {
		if err := cfg.HTTP.Serve(httpLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()
	cfg.Logger.Info("http server listening", "addr", httpLn.Addr().String())

	if cfg.GRPC != nil {
		grpcLn, err := lc.Listen(ctx, "tcp", cfg.GRPCAddr)
		if err != nil {
			_ = cfg.HTTP.Close()
			return fmt.Errorf("listen grpc %s: %w", cfg.GRPCAddr, err)
		}
		go func() {
			if err := cfg.GRPC.Serve(grpcLn); err != nil {
				errCh <- fmt.Errorf("grpc server: %w", err)
			}
		}()
		cfg.Logger.Info("grpc server listening", "addr", grpcLn.Addr().String())
	}

	var runErr error
	select {
	case <-ctx.Done():
		cfg.Logger.Info("shutting down")
	case runErr = <-errCh:
		cfg.Logger.Error("server failed, shutting down", "error", runErr)
	}

	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := cfg.HTTP.Shutdown(sctx); err != nil {
		cfg.Logger.Error("http shutdown", "error", err)
	}
	if cfg.GRPC != nil {
		stopped := make(chan struct{})
		go func() { cfg.GRPC.GracefulStop(); close(stopped) }()
		select {
		case <-stopped:
		case <-sctx.Done():
			cfg.GRPC.Stop()
		}
	}
	return runErr
}
