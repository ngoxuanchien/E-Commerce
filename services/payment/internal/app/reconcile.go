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

// ReconcileConfig sets the polling period, the minimum age of a payment worth polling and the budget of
// each provider call (default 15 s).
type ReconcileConfig struct{ Interval, MinAge, ProviderTimeout time.Duration }

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
	if cfg.ProviderTimeout <= 0 {
		cfg.ProviderTimeout = providerTimeout
	}
	return &Reconciler{store: store, providers: providers, obs: obs, cfg: cfg, now: now, logger: logger}
}

// RunOnce leases PENDING payments older than MinAge and expires those stuck past expires_at plus a
// 5 minute grace, then purges idempotency keys (24 h) and provider events (30 days). A provider error
// for one payment is logged and skipped; the next run retries it once the lease expires.
//
// The lease is what keeps two replicas off the same row without holding a row lock across a provider
// call, which would block the webhooks that ApplyObserved runs in their own transaction. A replica
// that crashes mid-run therefore delays its rows by one interval, never longer.
func (r *Reconciler) RunOnce(ctx context.Context) error {
	now := r.now()
	var pending []domain.Payment
	if err := r.store.InTx(ctx, func(tx Tx) error {
		var err error
		pending, err = tx.ClaimPendingForReconcile(ctx, now, now.Add(-r.cfg.MinAge), now.Add(r.cfg.Interval), reconcileBatch)
		return err
	}); err != nil {
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
	if _, err := r.store.PurgeProviderEventsBefore(ctx, now.Add(-providerEventTTL)); err != nil {
		return err
	}
	return nil
}

func (r *Reconciler) reconcile(ctx context.Context, p domain.Payment, now time.Time) error {
	prov, ok := r.providers.ByName(p.Provider)
	if !ok {
		return errors.New("no adapter for provider " + p.Provider)
	}
	if err := r.withProviderTimeout(ctx, func(callCtx context.Context) error {
		return queryAndApply(callCtx, r.obs, prov, p)
	}); err != nil {
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
	if err := r.withProviderTimeout(ctx, func(callCtx context.Context) error {
		_, err := cancelAndObserve(callCtx, r.obs, prov, cur)
		return err
	}); err != nil {
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

// withProviderTimeout runs fn under a fresh ProviderTimeout budget, so a slow Query cannot starve the
// Cancel that follows it: an expired payment must still reach EXPIRED when the provider is slow.
func (r *Reconciler) withProviderTimeout(ctx context.Context, fn func(context.Context) error) error {
	callCtx, cancel := context.WithTimeout(ctx, r.cfg.ProviderTimeout)
	defer cancel()
	return fn(callCtx)
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
