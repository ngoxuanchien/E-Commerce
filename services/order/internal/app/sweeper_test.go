package app_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	catalogv1 "github.com/ngoxuanchien/e-commerce/gen/catalog/v1"
	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/payment"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/payment/paymentfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// crashedCheckout stores a PENDING order created at `at` holding a stock
// reservation, as if the service died between Tx A and Tx C.
func crashedCheckout(t *testing.T, e *env, at time.Time, stock int32) (domain.Order, *catalogv1.SkuInfo) {
	t.Helper()
	s := e.fake.AddSku(catalogfake.NewShop("Crash"), 10_000, stock)
	skus, err := e.catalog.GetSkus(ctx, []string{s.GetSkuId()})
	require.NoError(t, err)
	orders, err := domain.BuildOrders(domain.CheckoutParams{
		CheckoutID: uid(), BuyerID: uid(), Lines: []domain.Line{{SkuID: s.GetSkuId(), Quantity: 2}}, Skus: skus,
		ShippingFee: 30_000, Address: testAddress, PaymentMethod: domain.PaymentCOD, Now: at, NewID: uid,
	})
	require.NoError(t, err)
	o := orders[0]
	require.NoError(t, e.store.InTx(ctx, func(tx app.Tx) error { return tx.InsertOrder(ctx, o) }))
	require.NoError(t, e.catalog.ReserveStock(ctx, o.ID, o.StockLines()))
	return o, s
}

// sweepDuringReserve runs `run` once, just before the first reservation.
type sweepDuringReserve struct {
	app.Catalog
	once sync.Once
	run  func()
}

func (c *sweepDuringReserve) ReserveStock(ctx context.Context, orderID string, lines []domain.Line) error {
	c.once.Do(c.run)
	return c.Catalog.ReserveStock(ctx, orderID, lines)
}

func TestSweeper(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true, redis: true})
	sw := app.NewSweeper(e.store, e.catalog, nil, app.SweeperConfig{
		Interval: time.Minute, PendingTimeout: 2 * time.Minute, BatchSize: 2, Now: e.clock.Now,
	})

	t.Run("stale pending orders are cancelled and released", func(t *testing.T) {
		o, s := crashedCheckout(t, e, e.clock.Now(), 5)
		require.Equal(t, int32(3), e.fake.Stock(s.GetSkuId()))
		e.clock.Advance(3 * time.Minute)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		got, err := e.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
		assert.Equal(t, domain.CancelCheckoutTimeout, *got.CancelReason)
		assert.Equal(t, domain.RoleSystem, got.History[1].ActorRole)
		assert.Equal(t, int32(5), e.fake.Stock(s.GetSkuId()))
		assert.False(t, e.fake.Reserved(o.ID))

		evs := e.events(t, o.ID)
		require.Equal(t, []event.Type{event.TypeOrderStatusChanged, event.TypeOrderCancelled}, types(evs))
		c, err := event.DecodeData[event.OrderCancelled](evs[1])
		require.NoError(t, err)
		assert.Equal(t, event.OrderCancelled{
			OrderID: o.ID, Reason: "CHECKOUT_TIMEOUT", CancelledBy: "system",
			CheckoutID: o.CheckoutID, PaymentMethod: domain.PaymentCOD,
		}, c)
	})

	t.Run("fresh pending and non-pending orders are left alone", func(t *testing.T) {
		old := e.seedOrder(t, uid(), catalogfake.NewShop("Old"), domain.StatusPendingConfirmation)
		e.clock.Advance(3 * time.Minute)
		fresh, _ := crashedCheckout(t, e, e.clock.Now(), 5)
		e.clock.Advance(time.Minute)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Zero(t, n)
		for id, want := range map[string]domain.Status{old.ID: domain.StatusPendingConfirmation, fresh.ID: domain.StatusPending} {
			got, err := e.store.GetOrder(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, want, got.Status)
		}
		e.clock.Advance(2 * time.Minute) // let `fresh` go stale and clean it up
		_, err = sw.RunOnce(ctx)
		require.NoError(t, err)
	})

	t.Run("release failures do not block cancellation", func(t *testing.T) {
		o, _ := crashedCheckout(t, e, e.clock.Now(), 5)
		e.clock.Advance(3 * time.Minute)
		e.fake.FailNext(catalogfake.ReleaseStock, codes.Unavailable, 3)

		_, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		got, err := e.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
		assert.True(t, e.fake.Reserved(o.ID), "catalog will release it when it consumes OrderCancelled")
		assert.Contains(t, types(e.events(t, o.ID)), event.TypeOrderCancelled)
	})

	t.Run("works through every batch", func(t *testing.T) {
		for range 5 {
			crashedCheckout(t, e, e.clock.Now(), 5)
		}
		e.clock.Advance(3 * time.Minute)
		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 5, n, "batch size 2 → 3 batches in one run")
	})

	t.Run("expired idempotency keys are purged", func(t *testing.T) {
		user, hash := uid(), []byte("h")
		_, _, err := e.store.BeginIdempotency(ctx, user, "k", hash, e.clock.Now(), time.Time{})
		require.NoError(t, err)
		e.clock.Advance(25 * time.Hour)
		_, err = sw.RunOnce(ctx)
		require.NoError(t, err)
		_, created, err := e.store.BeginIdempotency(ctx, user, "k", hash, e.clock.Now(), time.Time{})
		require.NoError(t, err)
		assert.True(t, created)
	})

	t.Run("a checkout that outlives PENDING_TIMEOUT loses to the sweeper", func(t *testing.T) {
		hook := &sweepDuringReserve{Catalog: e.catalog, run: func() {
			e.clock.Advance(3 * time.Minute)
			_, err := sw.RunOnce(ctx)
			assert.NoError(t, err)
		}}
		svc := app.NewCheckoutService(e.store, e.carts, hook, app.CheckoutConfig{ShippingFee: 30_000, Now: e.clock.Now})
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Late"), 10_000, 5)
		e.cartSet(t, buyer, s.GetSkuId(), 1)

		_, err := svc.Checkout(ctx, checkoutInput(buyer, "late"), (&recorder{}).encode)
		require.ErrorIs(t, err, app.ErrCheckoutExpired)
		orders := buyerOrders(t, e, buyer)
		require.Len(t, orders, 1)
		assert.Equal(t, domain.StatusCancelled, orders[0].Status)
		assert.Equal(t, domain.CancelCheckoutTimeout, *orders[0].CancelReason)
		assert.Equal(t, int32(5), e.fake.Stock(s.GetSkuId()))
		assert.False(t, e.fake.Reserved(orders[0].ID))
		lines, err := e.carts.Lines(ctx, buyer)
		require.NoError(t, err)
		assert.Len(t, lines, 1, "cart kept")
	})
}

// TestSweeperRunBoundsPendingLifetime checks acceptance criterion 2: no order
// stays PENDING longer than PENDING_TIMEOUT + one sweeper interval.
func TestSweeperRunBoundsPendingLifetime(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true})
	const timeout, interval = 400 * time.Millisecond, 200 * time.Millisecond
	realNow := func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	sw := app.NewSweeper(e.store, e.catalog, nil, app.SweeperConfig{Interval: interval, PendingTimeout: timeout, Now: realNow})
	o, _ := crashedCheckout(t, e, realNow(), 5)

	rctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- sw.Run(rctx) }()
	t.Cleanup(func() {
		cancel()
		assert.NoError(t, <-done)
	})

	var got domain.Order
	require.Eventually(t, func() bool {
		var err error
		got, err = e.store.GetOrder(ctx, o.ID)
		return err == nil && got.Status == domain.StatusCancelled
	}, timeout+interval+2*time.Second, 20*time.Millisecond)
	lifetime := got.History[len(got.History)-1].At.Sub(o.CreatedAt)
	assert.GreaterOrEqual(t, lifetime, timeout)
	assert.LessOrEqual(t, lifetime, timeout+interval+250*time.Millisecond, "250ms slack for scheduling and the transaction")
}

// TestSweeperSlowCatalogDoesNotDelayCancellation: while catalog is down each
// release burns its full retry budget; cancelling every stale order must not
// wait for those releases, and the releases must not run one by one.
func TestSweeperSlowCatalogDoesNotDelayCancellation(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true})
	sw := app.NewSweeper(e.store, e.catalog, nil, app.SweeperConfig{
		Interval: time.Minute, PendingTimeout: 2 * time.Minute, BatchSize: 2, Now: e.clock.Now,
	})
	var orders []domain.Order
	for range 4 {
		o, _ := crashedCheckout(t, e, e.clock.Now(), 5)
		orders = append(orders, o)
	}
	e.clock.Advance(3 * time.Minute)
	e.fake.SetDelay(catalogfake.ReleaseStock, time.Second) // > the 300ms per-attempt timeout

	start := time.Now()
	n, err := sw.RunOnce(ctx)
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	for _, o := range orders {
		got, err := e.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
	}
	// One release costs ~0.9s (3 attempts × 300ms); four in sequence ≈ 3.6s.
	assert.Less(t, elapsed, 2*time.Second, "releases must run concurrently")
}

// paymentsEnv wires a real app.Payments (payment.Client) to a fake, so the
// sweeper's payment-timeout step runs through the gRPC adapter.
type paymentsEnv struct {
	*env
	payFake *paymentfake.Fake
	store   app.Store
	pay     app.Payments
	// log collects the sweeper's warnings.
	log *bytes.Buffer
}

func newPaymentsEnv(t *testing.T) *paymentsEnv {
	e := newEnv(t, envOpts{postgres: true})
	f := paymentfake.New()
	log := &bytes.Buffer{}
	return &paymentsEnv{
		env: e, payFake: f, store: e.store, pay: payment.New(f.Dial(t), 200*time.Millisecond),
		log: log,
	}
}

// sweeper builds a sweeper for this env with cfg's zero values filled in.
func (p *paymentsEnv) sweeper(cfg app.SweeperConfig) *app.Sweeper {
	if cfg.Interval == 0 {
		cfg.Interval = time.Minute
	}
	if cfg.PendingTimeout == 0 {
		cfg.PendingTimeout = 2 * time.Minute
	}
	cfg.Now = p.clock.Now
	cfg.Logger = slog.New(slog.NewTextHandler(p.log, nil))
	return app.NewSweeper(p.store, p.catalog, p.pay, cfg)
}

// walk applies action by actor to o and persists it, like a request handler.
func walk(t *testing.T, e *env, o *domain.Order, action domain.Action, actor domain.Actor, at time.Time) {
	t.Helper()
	ch, err := domain.Transition(*o, action, actor)
	require.NoError(t, err)
	prev := o.Version
	o.Apply(ch, "", at)
	require.NoError(t, e.store.InTx(ctx, func(tx app.Tx) error { return tx.SaveTransition(ctx, *o, prev) }))
	o.Version = prev + 1
}

// TestSweeperPayments covers the payment-timeout step (WS3' §5.2).
func TestSweeperPayments(t *testing.T) {
	t.Run("NOT_PAID cancels every order of the checkout and releases stock", func(t *testing.T) {
		p := newPaymentsEnv(t)
		sw := p.sweeper(app.SweeperConfig{})
		orders := awaitingCheckout(t, p.env, 2, 30*time.Minute)
		checkoutID := orders[0].CheckoutID
		p.payFake.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)
		p.clock.Advance(31 * time.Minute)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, n)

		for _, want := range orders {
			got, err := p.store.GetOrder(ctx, want.ID)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusCancelled, got.Status)
			assert.Equal(t, domain.CancelPaymentTimeout, *got.CancelReason)
			assert.Equal(t, domain.RoleSystem, got.History[len(got.History)-1].ActorRole)
			assert.Equal(t, domain.PaymentUnpaid, got.PaymentStatus, "an unpaid order is not refunded")

			evs := p.events(t, want.ID)
			require.Equal(t, []event.Type{event.TypeOrderStatusChanged, event.TypeOrderCancelled}, types(evs))
			c, err := event.DecodeData[event.OrderCancelled](evs[1])
			require.NoError(t, err)
			assert.Equal(t, event.OrderCancelled{
				OrderID: want.ID, Reason: domain.CancelPaymentTimeout, CancelledBy: "system",
				CheckoutID: checkoutID, PaymentMethod: domain.PaymentVNPay,
			}, c)
		}
		assert.ElementsMatch(t, []string{orders[0].ID, orders[1].ID}, p.fake.ReleasedOrders())
		assert.Equal(t, 1, p.payFake.Calls(checkoutID), "the checkout is asked once")
	})

	t.Run("PAID leaves the orders untouched and warns after the grace", func(t *testing.T) {
		p := newPaymentsEnv(t)
		sw := p.sweeper(app.SweeperConfig{})
		orders := awaitingCheckout(t, p.env, 2, 30*time.Minute)
		checkoutID := orders[0].CheckoutID
		p.payFake.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_PAID)
		p.clock.Advance(31 * time.Minute)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Zero(t, n)
		for _, want := range orders {
			got, err := p.store.GetOrder(ctx, want.ID)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusAwaitingPayment, got.Status)
		}
		assert.Empty(t, p.fake.ReleasedOrders())
		assert.NotContains(t, p.log.String(), "still awaiting payment", "inside the 10 min grace")

		p.clock.Advance(11 * time.Minute)
		n, err = sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Zero(t, n)
		assert.Contains(t, p.log.String(), "still awaiting payment")
		assert.Contains(t, p.log.String(), checkoutID)
		assert.Empty(t, p.fake.ReleasedOrders())
	})

	t.Run("UNAVAILABLE retries on a later tick", func(t *testing.T) {
		p := newPaymentsEnv(t)
		sw := p.sweeper(app.SweeperConfig{})
		orders := awaitingCheckout(t, p.env, 1, 30*time.Minute)
		checkoutID := orders[0].CheckoutID
		p.payFake.SetError(checkoutID, codes.Unavailable)
		p.clock.Advance(31 * time.Minute)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err, "an unavailable payment-svc is not a sweep failure")
		assert.Zero(t, n)
		got, err := p.store.GetOrder(ctx, orders[0].ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusAwaitingPayment, got.Status)
		assert.Empty(t, p.fake.ReleasedOrders())

		p.payFake.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)
		n, err = sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		got, err = p.store.GetOrder(ctx, orders[0].ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
	})

	t.Run("a checkout that has not expired is not asked about", func(t *testing.T) {
		p := newPaymentsEnv(t)
		sw := p.sweeper(app.SweeperConfig{})
		orders := awaitingCheckout(t, p.env, 1, 30*time.Minute)
		checkoutID := orders[0].CheckoutID
		p.clock.Advance(29 * time.Minute)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Zero(t, n)
		assert.Zero(t, p.payFake.Calls(checkoutID), "the deadline has not passed")
		got, err := p.store.GetOrder(ctx, orders[0].ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusAwaitingPayment, got.Status)
	})

	t.Run("payment is asked before any row is locked", func(t *testing.T) {
		p := newPaymentsEnv(t)
		orders := awaitingCheckout(t, p.env, 1, 30*time.Minute)
		p.payFake.SetOutcome(orders[0].CheckoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)
		// The hook takes a row lock while payment-svc is being asked, and
		// releases it again: if the sweeper held a lock over the call, the
		// FOR UPDATE NOWAIT below would fail.
		hook := &lockDuringPayment{Payments: p.pay, Pool: p.pool, OrderID: orders[0].ID}
		sw := app.NewSweeper(p.store, p.catalog, hook, app.SweeperConfig{
			Interval: time.Minute, PendingTimeout: 2 * time.Minute, Now: p.clock.Now,
			Logger: slog.New(slog.NewTextHandler(p.log, nil)),
		})
		p.clock.Advance(31 * time.Minute)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		assert.NoError(t, hook.err, "the sweeper held no lock while calling payment-svc")
		got, err := p.store.GetOrder(ctx, orders[0].ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
	})

	t.Run("the payment-timeout step works through every batch", func(t *testing.T) {
		p := newPaymentsEnv(t)
		sw := p.sweeper(app.SweeperConfig{BatchSize: 2})
		var ids []string
		for range 5 {
			orders := awaitingCheckout(t, p.env, 1, 30*time.Minute)
			ids = append(ids, orders[0].ID)
			p.payFake.SetOutcome(orders[0].CheckoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)
		}
		p.clock.Advance(31 * time.Minute)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 5, n, "batch size 2 → 3 batches in one run")
		for _, id := range ids {
			got, err := p.store.GetOrder(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusCancelled, got.Status)
		}
	})

	t.Run("a nil payment client leaves expired checkouts alone", func(t *testing.T) {
		p := newPaymentsEnv(t)
		orders := awaitingCheckout(t, p.env, 1, 30*time.Minute)
		sw := app.NewSweeper(p.store, p.catalog, nil, app.SweeperConfig{
			Interval: time.Minute, PendingTimeout: 2 * time.Minute, Now: p.clock.Now,
			Logger: slog.New(slog.NewTextHandler(p.log, nil)),
		})
		p.clock.Advance(31 * time.Minute)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Zero(t, n)
		got, err := p.store.GetOrder(ctx, orders[0].ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusAwaitingPayment, got.Status)
	})
}

// lockDuringPayment takes and releases a row lock around the payment call, so
// a test can tell whether the sweeper locked anything before asking payment-svc.
type lockDuringPayment struct {
	app.Payments
	Pool    *pgxpool.Pool
	OrderID string
	err     error
}

func (l *lockDuringPayment) CancelCheckoutPayments(ctx context.Context, checkoutID string) (app.PaymentOutcome, error) {
	outcome, err := l.Payments.CancelCheckoutPayments(ctx, checkoutID)
	tx, terr := l.Pool.Begin(ctx)
	if terr != nil {
		l.err = terr
		return outcome, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, qerr := tx.Exec(ctx, `SELECT id FROM orders WHERE id = $1 FOR UPDATE NOWAIT`, l.OrderID); qerr != nil {
		l.err = qerr
	}
	return outcome, err
}

// TestSweeperAutoComplete covers the auto-complete step (WS3' §5.3).
func TestSweeperAutoComplete(t *testing.T) {
	// deliveredOrder walks o to DELIVERED at e.clock.Now().
	deliveredOrder := func(t *testing.T, e *env, o *domain.Order) {
		t.Helper()
		seller := domain.Actor{ID: o.SellerUserID, Role: domain.RoleSeller}
		walk(t, e, o, domain.ActionMarkPaid, domain.SystemActor(), e.clock.Now())
		walk(t, e, o, domain.ActionConfirm, seller, e.clock.Now())
		walk(t, e, o, domain.ActionShip, seller, e.clock.Now())
		walk(t, e, o, domain.ActionDeliver, seller, e.clock.Now())
		require.Equal(t, domain.StatusDelivered, o.Status)
		require.NotNil(t, o.DeliveredAt)
	}

	t.Run("completed by the system after the deadline", func(t *testing.T) {
		p := newPaymentsEnv(t)
		sw := p.sweeper(app.SweeperConfig{AutoCompleteAfter: 72 * time.Hour})

		cod := p.seedOrder(t, uid(), catalogfake.NewShop("COD"), domain.StatusDelivered)
		online := awaitingCheckout(t, p.env, 1, 30*time.Minute)[0]
		deliveredOrder(t, p.env, &online)
		assert.Equal(t, domain.PaymentPaid, online.PaymentStatus)

		p.clock.Advance(71 * time.Hour)
		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Zero(t, n, "the 72 h deadline has not passed")
		got, err := p.store.GetOrder(ctx, cod.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusDelivered, got.Status)

		p.clock.Advance(2 * time.Hour)
		n, err = sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, n)
		for _, want := range []domain.Order{cod, online} {
			got, err := p.store.GetOrder(ctx, want.ID)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusCompleted, got.Status)
			assert.Equal(t, domain.RoleSystem, got.History[len(got.History)-1].ActorRole)
			require.NotNil(t, got.DeliveredAt, "delivered_at is kept")

			evs := p.events(t, want.ID)
			require.Equal(t, []event.Type{event.TypeOrderStatusChanged, event.TypeOrderCompleted}, types(evs))
			c, err := event.DecodeData[event.OrderCompleted](evs[1])
			require.NoError(t, err)
			assert.Equal(t, "system", c.CompletedBy)
			assert.Equal(t, want.Total, c.Total)
		}
	})

	t.Run("auto-complete works through every batch", func(t *testing.T) {
		p := newPaymentsEnv(t)
		sw := p.sweeper(app.SweeperConfig{AutoCompleteAfter: time.Hour, BatchSize: 2})
		var orders []domain.Order
		for i := range 5 {
			o := p.seedOrder(t, uid(), catalogfake.NewShop(fmt.Sprintf("Batch %d", i)), domain.StatusDelivered)
			orders = append(orders, o)
		}
		p.clock.Advance(2 * time.Hour)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 5, n, "batch size 2 → 3 batches in one run")
		for _, want := range orders {
			got, err := p.store.GetOrder(ctx, want.ID)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusCompleted, got.Status)
		}
	})

	t.Run("AutoCompleteAfter 0 disables the step", func(t *testing.T) {
		p := newPaymentsEnv(t)
		sw := p.sweeper(app.SweeperConfig{})
		o := p.seedOrder(t, uid(), catalogfake.NewShop("Disabled"), domain.StatusDelivered)
		p.clock.Advance(365 * 24 * time.Hour)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Zero(t, n)
		got, err := p.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusDelivered, got.Status)
	})
}

// TestSweeperSlowPaymentDoesNotStallTheSweep (final review I1): a payment-svc
// that accepts calls but answers after the client timeout must cost about one
// timeout per tick, not one per expired checkout, and the steps after the
// payment step must still run in the same RunOnce.
func TestSweeperSlowPaymentDoesNotStallTheSweep(t *testing.T) {
	p := newPaymentsEnv(t) // 200 ms client timeout
	sw := p.sweeper(app.SweeperConfig{AutoCompleteAfter: 10 * time.Minute})
	const expired = 30 // sequentially 30 × 200 ms = 6 s
	var awaiting []domain.Order
	for range expired {
		o := awaitingCheckout(t, p.env, 1, 30*time.Minute)[0]
		p.payFake.SetOutcome(o.CheckoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)
		awaiting = append(awaiting, o)
	}
	stale, _ := crashedCheckout(t, p.env, p.clock.Now(), 5)
	delivered := p.seedOrder(t, uid(), catalogfake.NewShop("Slow pay"), domain.StatusDelivered)
	p.clock.Advance(31 * time.Minute)
	p.payFake.SetDelay(time.Second) // > the 200 ms client timeout

	start := time.Now()
	n, err := sw.RunOnce(ctx)
	elapsed := time.Since(start)
	require.NoError(t, err, "a slow payment-svc is not a sweep failure")
	assert.Less(t, elapsed, 2*time.Second, "one tick must not cost one timeout per checkout")
	assert.Equal(t, 2, n, "the stale PENDING order and the delivered order")

	calls := 0
	for _, o := range awaiting {
		calls += p.payFake.Calls(o.CheckoutID)
		got, err := p.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusAwaitingPayment, got.Status)
	}
	assert.LessOrEqual(t, calls, 10, "the step stops asking after the first timeout")
	assert.Positive(t, calls)
	got, err := p.store.GetOrder(ctx, stale.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCancelled, got.Status)
	got, err = p.store.GetOrder(ctx, delivered.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCompleted, got.Status, "auto-complete ran in the same RunOnce")

	p.payFake.SetDelay(0)
	n, err = sw.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, expired, n, "the next tick asks again")
}

// A plain UNAVAILABLE answer is per checkout (payment-svc also returns it for a
// checkout that is still payable), so it must not stop the rest of the tick.
func TestSweeperUnavailableCheckoutDoesNotStopOthers(t *testing.T) {
	p := newPaymentsEnv(t)
	sw := p.sweeper(app.SweeperConfig{})
	payable := awaitingCheckout(t, p.env, 1, 29*time.Minute)[0] // earliest deadline
	p.payFake.SetError(payable.CheckoutID, codes.Unavailable)
	var unpaid []domain.Order
	for range 3 {
		o := awaitingCheckout(t, p.env, 1, 30*time.Minute)[0]
		p.payFake.SetOutcome(o.CheckoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)
		unpaid = append(unpaid, o)
	}
	p.clock.Advance(31 * time.Minute)

	n, err := sw.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	for _, o := range unpaid {
		got, err := p.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
	}
	got, err := p.store.GetOrder(ctx, payable.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusAwaitingPayment, got.Status)
}

// failingExpiredCheckouts breaks the payment-timeout step's paging query.
type failingExpiredCheckouts struct{ app.Store }

var errExpiredCheckouts = errors.New("expired checkouts query failed")

func (failingExpiredCheckouts) ExpiredCheckouts(context.Context, time.Time, *app.CheckoutCursor, int) ([]app.CheckoutCursor, error) {
	return nil, errExpiredCheckouts
}

// TestSweeperFailingStepDoesNotStopLaterSteps: every step runs every tick; the
// errors are joined and the counts summed.
func TestSweeperFailingStepDoesNotStopLaterSteps(t *testing.T) {
	p := newPaymentsEnv(t)
	sw := app.NewSweeper(failingExpiredCheckouts{p.store}, p.catalog, p.pay, app.SweeperConfig{
		Interval: time.Minute, PendingTimeout: 2 * time.Minute, AutoCompleteAfter: 10 * time.Minute,
		Now: p.clock.Now, Logger: slog.New(slog.NewTextHandler(p.log, nil)),
	})
	stale, _ := crashedCheckout(t, p.env, p.clock.Now(), 5)
	delivered := p.seedOrder(t, uid(), catalogfake.NewShop("Failing step"), domain.StatusDelivered)
	user, hash := uid(), []byte("h")
	_, _, err := p.store.BeginIdempotency(ctx, user, "k", hash, p.clock.Now(), time.Time{})
	require.NoError(t, err)
	p.clock.Advance(25 * time.Hour)

	n, err := sw.RunOnce(ctx)
	require.ErrorIs(t, err, errExpiredCheckouts)
	assert.Equal(t, 2, n)
	got, err := p.store.GetOrder(ctx, stale.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCancelled, got.Status)
	got, err = p.store.GetOrder(ctx, delivered.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCompleted, got.Status, "auto-complete runs after a failed payment step")
	_, created, err := p.store.BeginIdempotency(ctx, user, "k", hash, p.clock.Now(), time.Time{})
	require.NoError(t, err)
	assert.True(t, created, "the purge runs after a failed payment step")
}
