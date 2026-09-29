package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// SweeperConfig configures Sweeper. Zero BatchSize / IdempotencyTTL use 100 / 24h.
type SweeperConfig struct {
	Interval       time.Duration // SWEEPER_INTERVAL, > 0
	PendingTimeout time.Duration // PENDING_TIMEOUT
	BatchSize      int
	IdempotencyTTL time.Duration
	Now            func() time.Time
	Logger         *slog.Logger
}

// Sweeper cancels orders stuck in PENDING (a checkout that crashed between
// Tx A and Tx C) and purges expired idempotency keys (WS3 §4). Replicas may
// run it concurrently: stale orders are claimed with FOR UPDATE SKIP LOCKED.
type Sweeper struct {
	store   Store
	catalog Catalog
	cfg     SweeperConfig
}

func NewSweeper(store Store, catalog Catalog, cfg SweeperConfig) *Sweeper {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.IdempotencyTTL <= 0 {
		cfg.IdempotencyTTL = 24 * time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = defaultNow
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Sweeper{store: store, catalog: catalog, cfg: cfg}
}

// Run sweeps immediately and then every Interval until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		if _, err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
			s.cfg.Logger.ErrorContext(ctx, "sweeper run failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce cancels every stale PENDING order batch by batch (system,
// CHECKOUT_TIMEOUT, with events), then releases their stock outside the row
// locks (best effort, concurrently), then purges expired idempotency keys. It
// returns how many orders it cancelled.
func (s *Sweeper) RunOnce(ctx context.Context) (int, error) {
	var cancelled []domain.Order
	for {
		now := s.cfg.Now()
		var batch []domain.Order
		err := s.store.InTx(ctx, func(tx Tx) error {
			orders, err := tx.ClaimStalePending(ctx, now.Add(-s.cfg.PendingTimeout), s.cfg.BatchSize)
			if err != nil {
				return err
			}
			for i := range orders {
				if err := applyTransition(ctx, tx, &orders[i], domain.ActionCancel, domain.SystemActor(), domain.CancelCheckoutTimeout, now); err != nil {
					return err
				}
			}
			batch = orders
			return nil
		})
		if err != nil {
			s.releaseAll(ctx, cancelled)
			return len(cancelled), err
		}
		cancelled = append(cancelled, batch...)
		if len(batch) < s.cfg.BatchSize {
			break
		}
	}
	s.releaseAll(ctx, cancelled)
	if _, err := s.store.DeleteIdempotencyKeysBefore(ctx, s.cfg.Now().Add(-s.cfg.IdempotencyTTL)); err != nil {
		return len(cancelled), err
	}
	return len(cancelled), nil
}

// releaseConcurrency bounds the parallel ReleaseStock calls of one sweep.
const releaseConcurrency = 10

// releaseAll returns the stock of already-cancelled orders, best effort and
// concurrently, so a slow catalog costs about one retry budget per sweep
// rather than one per order. Failures are only logged: catalog also releases
// the stock when it consumes OrderCancelled.
func (s *Sweeper) releaseAll(ctx context.Context, orders []domain.Order) {
	sem := make(chan struct{}, releaseConcurrency)
	var wg sync.WaitGroup
	for _, o := range orders {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			if err := s.catalog.ReleaseStock(ctx, o.ID); err != nil {
				s.cfg.Logger.WarnContext(ctx, "release stock of timed-out order failed; catalog releases it on OrderCancelled",
					"order_id", o.ID, "error", err)
			}
		}()
	}
	wg.Wait()
}
