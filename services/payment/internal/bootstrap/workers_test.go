package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestSuperviseWorkersFailureCancelsTheSharedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	boom := errors.New("boom")
	stopped := make(chan struct{})

	err := superviseWorkers(ctx, cancel, quiet, []worker{
		{"healthy", func(ctx context.Context) error { <-ctx.Done(); close(stopped); return ctx.Err() }},
		{"failing", func(context.Context) error { time.Sleep(20 * time.Millisecond); return boom }},
	})

	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "failing")
	assert.Error(t, ctx.Err(), "the shared context is cancelled")
	select {
	case <-stopped:
	default:
		t.Fatal("the healthy worker was not stopped and awaited")
	}
}

func TestSuperviseWorkersCleanShutdownIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- superviseWorkers(ctx, cancel, quiet, []worker{
			{"w", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }},
		})
	}()
	cancel()
	require.NoError(t, <-done)
}

func TestSuperviseWorkersUnexpectedNilReturnIsFatal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := superviseWorkers(ctx, cancel, quiet, []worker{{"quitter", func(context.Context) error { return nil }}})
	require.Error(t, err)
	assert.Error(t, ctx.Err())
}
