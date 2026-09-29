package app

import (
	"context"
	"log/slog"
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
// locks (best effort), then purges expired idempotency keys. It returns how
// many orders it cancelled.
func (s *Sweeper) RunOnce(ctx context.Context) (int, error) {
	total := 0
	for {
		now := s.cfg.Now()
		var cancelled []domain.Order
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
			cancelled = orders
			return nil
		})
		if err != nil {
			return total, err
		}
		for _, o := range cancelled {
			if err := s.catalog.ReleaseStock(ctx, o.ID); err != nil {
				s.cfg.Logger.WarnContext(ctx, "release stock of timed-out order failed; catalog releases it on OrderCancelled",
					"order_id", o.ID, "error", err)
			}
		}
		total += len(cancelled)
		if len(cancelled) < s.cfg.BatchSize {
			break
		}
	}
	if _, err := s.store.DeleteIdempotencyKeysBefore(ctx, s.cfg.Now().Add(-s.cfg.IdempotencyTTL)); err != nil {
		return total, err
	}
	return total, nil
}
