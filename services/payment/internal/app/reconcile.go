package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

const (
	reconcileBatch           = 200
	defaultReconcileInterval = time.Minute
	expiryGrace              = 5 * time.Minute
	idempotencyKeyTTL        = 24 * time.Hour
	providerEventTTL         = 30 * 24 * time.Hour
)

// ReconcileConfig sets the polling period and the minimum age of a payment worth polling.
type ReconcileConfig struct{ Interval, MinAge time.Duration }

// Reconciler repairs payments whose webhook never arrived and purges old dedupe rows (spec §6).
type Reconciler struct {
	store     Store
	providers Providers
	obs       *Observer
	cfg       ReconcileConfig
	now       func() time.Time
	logger    *slog.Logger
}

func NewReconciler(store Store, providers Providers, obs *Observer, cfg ReconcileConfig, now func() time.Time, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultReconcileInterval
	}
	return &Reconciler{store: store, providers: providers, obs: obs, cfg: cfg, now: now, logger: logger}
}

// RunOnce polls PENDING payments older than MinAge and expires those stuck past expires_at plus a
// 5 minute grace, then purges idempotency keys (24 h) and provider events (30 days). A provider error
// for one payment is logged and skipped; the next run retries it.
func (r *Reconciler) RunOnce(ctx context.Context) error {
	now := r.now()
	pending, err := r.store.ListPendingOlderThan(ctx, now.Add(-r.cfg.MinAge), reconcileBatch)
	if err != nil {
		return err
	}
	for _, p := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.reconcile(ctx, p, now); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.logger.ErrorContext(ctx, "reconcile payment failed", "payment_id", p.ID, "provider", p.Provider, "error", err)
		}
	}
	if _, err := r.store.PurgeIdempotencyKeysBefore(ctx, now.Add(-idempotencyKeyTTL)); err != nil {
		return err
	}
	_, err = r.store.PurgeProviderEventsBefore(ctx, now.Add(-providerEventTTL))
	return err
}

func (r *Reconciler) reconcile(ctx context.Context, p domain.Payment, now time.Time) error {
	prov, ok := r.providers.ByName(p.Provider)
	if !ok {
		return errors.New("no adapter for provider " + p.Provider)
	}
	callCtx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	if err := queryAndApply(callCtx, r.obs, prov, p); err != nil {
		return err
	}
	if !now.After(p.ExpiresAt.Add(expiryGrace)) {
		return nil
	}
	cur, err := r.store.GetPayment(ctx, p.ID)
	if err != nil {
		return err
	}
	if cur.Status != domain.StatusPending {
		return nil
	}
	if _, err := cancelAndObserve(callCtx, r.obs, prov, cur); err != nil {
		return err
	}
	cur, err = r.store.GetPayment(ctx, p.ID)
	if err != nil {
		return err
	}
	if cur.Status != domain.StatusPending {
		return nil
	}
	_, err = r.obs.Expire(ctx, p.ID)
	return err
}

// Run calls RunOnce every Interval until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) error {
	t := time.NewTicker(r.cfg.Interval)
	defer t.Stop()
	for {
		if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			r.logger.ErrorContext(ctx, "reconciler run failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
