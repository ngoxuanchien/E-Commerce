package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// defaultBatchSize is the WS3' §5 batch size of every sweeper step.
const defaultBatchSize = 50

// defaultPaidGrace is how long a paid checkout may stay AWAITING_PAYMENT past
// its deadline before the sweeper warns about it (the late-capture consumer
// marks it paid).
const defaultPaidGrace = 10 * time.Minute

// SweeperConfig configures Sweeper. Zero BatchSize / IdempotencyTTL use
// 50 / 24h; a zero PaidGrace uses 10 min; AutoCompleteAfter 0 disables
// auto-complete.
type SweeperConfig struct {
	Interval       time.Duration // SWEEPER_INTERVAL, > 0
	PendingTimeout time.Duration // PENDING_TIMEOUT
	BatchSize      int
	IdempotencyTTL time.Duration
	// AutoCompleteAfter is how long a DELIVERED order stays open before the
	// system completes it (AUTO_COMPLETE_AFTER); 0 disables the step.
	AutoCompleteAfter time.Duration
	// PaidGrace is how long a paid checkout may stay AWAITING_PAYMENT past
	// its deadline before the sweeper warns; 0 uses 10 min.
	PaidGrace time.Duration
	Now       func() time.Time
	Logger    *slog.Logger
}

// Sweeper cancels orders stuck in PENDING (a checkout that crashed between
// Tx A and Tx C), times out the checkouts whose payment never arrived,
// completes delivered orders the buyer never confirmed and purges expired
// idempotency keys (WS3 §4, WS3' §5). Replicas may run it concurrently: stale
// orders are claimed with FOR UPDATE SKIP LOCKED.
type Sweeper struct {
	store    Store
	catalog  Catalog
	payments Payments // nil disables the payment-timeout step
	cfg      SweeperConfig
}

func NewSweeper(store Store, catalog Catalog, payments Payments, cfg SweeperConfig) *Sweeper {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.IdempotencyTTL <= 0 {
		cfg.IdempotencyTTL = 24 * time.Hour
	}
	if cfg.PaidGrace <= 0 {
		cfg.PaidGrace = defaultPaidGrace
	}
	if cfg.Now == nil {
		cfg.Now = defaultNow
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Sweeper{store: store, catalog: catalog, payments: payments, cfg: cfg}
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

// RunOnce runs every sweep step in order — stale PENDING orders, expired
// checkouts, delivered orders past their auto-complete deadline, idempotency
// keys — and returns how many orders it cancelled or completed. Every step
// runs even when an earlier one failed, so one broken step cannot starve the
// others; the errors are joined.
func (s *Sweeper) RunOnce(ctx context.Context) (int, error) {
	cancelled, errPending := s.cancelStalePending(ctx)
	timedOut, errPayments := s.timeoutPayments(ctx)
	completed, errComplete := s.autoComplete(ctx)
	_, errPurge := s.store.DeleteIdempotencyKeysBefore(ctx, s.cfg.Now().Add(-s.cfg.IdempotencyTTL))
	return cancelled + timedOut + completed, errors.Join(errPending, errPayments, errComplete, errPurge)
}

// cancelStalePending cancels every PENDING order created before
// PendingTimeout (system, CHECKOUT_TIMEOUT, with events) batch by batch and
// releases their stock outside the row locks (best effort, concurrently).
func (s *Sweeper) cancelStalePending(ctx context.Context) (int, error) {
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
	return len(cancelled), nil
}

// paymentConcurrency bounds the parallel CancelCheckoutPayments calls of one
// sweep, like releaseConcurrency.
const paymentConcurrency = 10

// errStopPayments tells timeoutPayments that payment-svc timed out (or the
// sweep was cancelled), so no further checkout is asked about this tick.
var errStopPayments = errors.New("payment-svc timed out; stop asking this tick")

// timeoutPayments cancels the AWAITING_PAYMENT checkouts past their deadline
// that payment-svc confirms are unpaid (WS3' §5.2). payment-svc is asked
// before any row is locked, for up to paymentConcurrency checkouts at once; a
// PAID or unavailable answer leaves the orders alone, and the checkout is
// re-evaluated on the next tick. The first call that times out stops the step
// for the rest of the tick, so a black-holed payment-svc costs about one call
// timeout per tick rather than one per expired checkout. A plain UNAVAILABLE
// answer does not: payment-svc also gives it for a checkout that is still
// payable, so it concerns only that checkout.
func (s *Sweeper) timeoutPayments(ctx context.Context) (int, error) {
	if s.payments == nil {
		return 0, nil
	}
	now := s.cfg.Now()
	var after *CheckoutCursor
	n := 0
	for {
		batch, err := s.store.ExpiredCheckouts(ctx, now, after, s.cfg.BatchSize)
		if err != nil {
			return n, err
		}
		cancelled, stopped, err := s.timeoutBatch(ctx, batch, now)
		n += cancelled
		if err != nil || stopped || len(batch) < s.cfg.BatchSize {
			return n, err
		}
		after = &batch[len(batch)-1]
	}
}

// timeoutBatch runs timeoutCheckout for every checkout of batch, at most
// paymentConcurrency at a time. Once a call reports errStopPayments no further
// checkout is started (calls already in flight finish) and stopped is true.
// The other errors are joined.
func (s *Sweeper) timeoutBatch(ctx context.Context, batch []CheckoutCursor, now time.Time) (n int, stopped bool, err error) {
	var (
		mu   sync.Mutex
		errs []error
		stop atomic.Bool
		wg   sync.WaitGroup
	)
	sem := make(chan struct{}, paymentConcurrency)
	for _, c := range batch {
		sem <- struct{}{}
		if stop.Load() {
			<-sem
			break
		}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			cancelled, err := s.timeoutCheckout(ctx, c, now)
			if errors.Is(err, errStopPayments) {
				stop.Store(true) // before the deferred release, so the next dispatch sees it
				return
			}
			mu.Lock()
			defer mu.Unlock()
			n += cancelled
			if err != nil {
				errs = append(errs, err)
			}
		}()
	}
	wg.Wait()
	return n, stop.Load(), errors.Join(errs...)
}

func (s *Sweeper) timeoutCheckout(ctx context.Context, c CheckoutCursor, now time.Time) (int, error) {
	outcome, err := s.payments.CancelCheckoutPayments(ctx, c.CheckoutID)
	switch {
	case err != nil && (errors.Is(err, ErrPaymentTimeout) || ctx.Err() != nil):
		s.cfg.Logger.WarnContext(ctx, "payment-svc timed out; remaining payment timeouts retried next tick",
			"checkout_id", c.CheckoutID, "error", err)
		return 0, errStopPayments
	case err != nil:
		s.cfg.Logger.WarnContext(ctx, "payment-svc unavailable; payment timeout retried next tick",
			"checkout_id", c.CheckoutID, "error", err)
		return 0, nil
	case outcome == OutcomePaid:
		if now.Sub(c.Deadline) > s.cfg.PaidGrace {
			s.cfg.Logger.WarnContext(ctx, "paid checkout still awaiting payment after its deadline",
				"checkout_id", c.CheckoutID, "deadline", c.Deadline)
		}
		return 0, nil
	}
	var cancelled []domain.Order
	err = s.store.InTx(ctx, func(tx Tx) error {
		orders, err := tx.LockAwaitingPayment(ctx, c.CheckoutID, now)
		if err != nil {
			return err
		}
		for i := range orders {
			if err := applyTransition(ctx, tx, &orders[i], domain.ActionCancel, domain.SystemActor(), domain.CancelPaymentTimeout, now); err != nil {
				return err
			}
		}
		cancelled = orders
		return nil
	})
	if errors.Is(err, ErrConcurrentUpdate) {
		return 0, nil // an order moved concurrently; the next tick re-evaluates the checkout
	}
	if err != nil {
		return 0, err
	}
	s.releaseAll(ctx, cancelled)
	return len(cancelled), nil
}

// autoComplete completes the orders delivered more than AutoCompleteAfter ago
// that the buyer never confirmed (system, with OrderCompleted). It does
// nothing when AutoCompleteAfter is 0.
func (s *Sweeper) autoComplete(ctx context.Context) (int, error) {
	if s.cfg.AutoCompleteAfter <= 0 {
		return 0, nil
	}
	n := 0
	for {
		now := s.cfg.Now()
		var batch []domain.Order
		err := s.store.InTx(ctx, func(tx Tx) error {
			orders, err := tx.ClaimDueDelivered(ctx, now.Add(-s.cfg.AutoCompleteAfter), s.cfg.BatchSize)
			if err != nil {
				return err
			}
			for i := range orders {
				if err := applyTransition(ctx, tx, &orders[i], domain.ActionComplete, domain.SystemActor(), "", now); err != nil {
					return err
				}
			}
			batch = orders
			return nil
		})
		if err != nil {
			return n, err
		}
		n += len(batch)
		if len(batch) < s.cfg.BatchSize {
			return n, nil
		}
	}
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
