// Package bootstrap wires ledger-svc's adapters, use cases, HTTP API and
// background workers. cmd/server and the end-to-end test share it.
package bootstrap

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/outbox"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/bank"
	httpadapter "github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/http"
	kafkaadapter "github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/kafka"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
)

// Config holds the tunables (WS0' §8 environment).
type Config struct {
	Brokers         []string // empty = no consumer (HTTP-only tests)
	PayoutMinAmount int64
	SettleInterval  time.Duration
	SettleDelay     time.Duration
	PendingInterval time.Duration
	RelayInterval   time.Duration // 0 = outbox default (500ms)
}

// Deps are the live connections.
type Deps struct {
	Pool     *pgxpool.Pool
	Producer outbox.Publisher
	Bank     app.Bank         // nil = bank.Simulator
	Logger   *slog.Logger     // nil = slog.Default()
	Now      func() time.Time // nil = UTC wall clock, µs precision
}

// App is the wired service.
type App struct {
	API      http.Handler // /api routes; wrap with authctx.Middleware
	Events   *app.EventHandler
	relay    *outbox.Relay
	settle   *app.SettleJob
	pending  *app.PendingJob
	consumer *kafkax.Consumer
}

func New(cfg Config, d Deps) (*App, error) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	}
	if d.Bank == nil {
		d.Bank = bank.Simulator{}
	}
	store := postgres.NewStore(d.Pool)
	events := app.NewEventHandler(store, d.Logger, d.Now)
	payouts := app.NewPayoutService(store, app.PayoutConfig{MinAmount: cfg.PayoutMinAmount, Now: d.Now, Logger: d.Logger})
	a := &App{
		API:    httpadapter.NewRouter(httpadapter.Services{Wallet: app.NewWalletService(store), Payouts: payouts}),
		Events: events,
		relay:  outbox.NewRelay(d.Pool, d.Producer, outbox.RelayConfig{Interval: cfg.RelayInterval, Logger: d.Logger}),
		settle: app.NewSettleJob(store, d.Bank, app.SettleConfig{
			Interval: cfg.SettleInterval, Delay: cfg.SettleDelay, Now: d.Now, Logger: d.Logger,
		}),
		pending: app.NewPendingJob(store, events, app.PendingConfig{Interval: cfg.PendingInterval, Now: d.Now, Logger: d.Logger}),
	}
	if len(cfg.Brokers) > 0 {
		c, err := kafkaadapter.NewConsumer(cfg.Brokers, kafkaadapter.NewHandler(events, d.Logger), d.Logger)
		if err != nil {
			return nil, err
		}
		a.consumer = c
	}
	return a, nil
}

// RunWorkers runs the consumer, the outbox relay, the settle job and the
// pending-postings job until ctx is cancelled, then closes the consumer.
func (a *App) RunWorkers(ctx context.Context) {
	var wg sync.WaitGroup
	run := func(f func(context.Context) error) {
		wg.Add(1)
		go func() { defer wg.Done(); _ = f(ctx) }()
	}
	run(a.relay.Run)
	run(a.settle.Run)
	run(a.pending.Run)
	if a.consumer != nil {
		run(a.consumer.Run)
	}
	wg.Wait()
	if a.consumer != nil {
		a.consumer.Close()
	}
}
