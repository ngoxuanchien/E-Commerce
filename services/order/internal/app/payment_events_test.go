package app_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

var quietLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// paymentSucceeded builds the PaymentSucceeded of a captured payment that
// covers every one of orders. Without orders it is a duplicate charge, which
// payment-svc refunds itself.
func paymentSucceeded(checkoutID, paymentID string, orders ...domain.Order) event.PaymentSucceeded {
	ev := event.PaymentSucceeded{
		PaymentID: paymentID, CheckoutID: checkoutID,
		Provider: event.ProviderVNPay, ProviderRef: "vnpay-" + paymentID,
		Allocations: make([]event.Allocation, 0, len(orders)),
	}
	if len(orders) > 0 {
		ev.BuyerID = orders[0].BuyerID
	}
	for _, o := range orders {
		ev.Amount += o.Total
		ev.Allocations = append(ev.Allocations, event.Allocation{
			OrderID: o.ID, SellerUserID: o.SellerUserID,
			Amount: o.Total, CommissionAmount: o.CommissionAmount,
		})
	}
	return ev
}

// paymentRefunded builds the PaymentRefunded of a fully refunded payment; a nil
// orderID is the refund of a duplicate charge.
func paymentRefunded(checkoutID string, orderID *string) event.PaymentRefunded {
	return event.PaymentRefunded{
		RefundID: uid(), PaymentID: uid(), CheckoutID: checkoutID, OrderID: orderID,
		Amount: 50_000, Provider: event.ProviderVNPay,
	}
}

func processed(t *testing.T, e *env, eventID string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM processed_events WHERE event_id = $1)`, eventID).Scan(&ok))
	return ok
}

func outboxCount(t *testing.T, e *env) int {
	t.Helper()
	var n int
	require.NoError(t, e.pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&n))
	return n
}

// countTo counts history entries that reach status.
func countTo(o domain.Order, status domain.Status) int {
	n := 0
	for _, h := range o.History {
		if h.To == status {
			n++
		}
	}
	return n
}

func TestPaymentEventHandler(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true})
	h := app.NewPaymentEventHandler(e.store, e.clock.Now, quietLogger)

	t.Run("success moves every allocated order to PENDING_CONFIRMATION/PAID", func(t *testing.T) {
		orders := awaitingCheckout(t, e, 2, 30*time.Minute)
		paymentID := uid()

		require.NoError(t, h.PaymentSucceeded(ctx, uid(), paymentSucceeded(orders[0].CheckoutID, paymentID, orders...)))

		for _, want := range orders {
			got, err := e.store.GetOrder(ctx, want.ID)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusPendingConfirmation, got.Status)
			assert.Equal(t, domain.PaymentPaid, got.PaymentStatus)

			last := got.History[len(got.History)-1]
			assert.Equal(t, domain.StatusPendingConfirmation, last.To)
			assert.Equal(t, domain.RoleSystem, last.ActorRole)
			require.NotNil(t, last.Note, "the payment id is the transition note")
			assert.Equal(t, paymentID, *last.Note)

			evs := e.events(t, want.ID)
			require.Equal(t, []event.Type{event.TypeOrderStatusChanged}, types(evs))
			changed, err := event.DecodeData[event.OrderStatusChanged](evs[0])
			require.NoError(t, err)
			assert.Equal(t, event.OrderStatusChanged{
				OrderID: want.ID, From: "AWAITING_PAYMENT", To: "PENDING_CONFIRMATION", ActorRole: "system",
			}, changed)
		}
	})

	t.Run("redelivery is a no-op", func(t *testing.T) {
		orders := awaitingCheckout(t, e, 2, 30*time.Minute)
		ev := paymentSucceeded(orders[0].CheckoutID, uid(), orders...)
		eventID := uid()

		require.NoError(t, h.PaymentSucceeded(ctx, eventID, ev))
		first := map[string]domain.Order{}
		for _, want := range orders {
			got, err := e.store.GetOrder(ctx, want.ID)
			require.NoError(t, err)
			first[want.ID] = got
			assert.Equal(t, 1, countTo(got, domain.StatusPendingConfirmation))
		}

		require.NoError(t, h.PaymentSucceeded(ctx, eventID, ev))
		for _, want := range orders {
			got, err := e.store.GetOrder(ctx, want.ID)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusPendingConfirmation, got.Status)
			assert.Equal(t, first[want.ID].Version, got.Version, "the second call changes nothing")
			assert.Len(t, got.History, len(first[want.ID].History))
			assert.Equal(t, 1, countTo(got, domain.StatusPendingConfirmation))
			assert.Len(t, e.events(t, want.ID), 1)
		}
	})

	t.Run("empty allocations change nothing", func(t *testing.T) {
		o := awaitingCheckout(t, e, 1, 30*time.Minute)[0]
		before, err := e.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		eventID := uid()

		duplicate := paymentSucceeded(o.CheckoutID, uid())
		require.NoError(t, h.PaymentSucceeded(ctx, eventID, duplicate))

		got, err := e.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusAwaitingPayment, got.Status)
		assert.Equal(t, domain.PaymentUnpaid, got.PaymentStatus)
		assert.Equal(t, before.Version, got.Version)
		assert.Empty(t, e.events(t, o.ID))
		assert.True(t, processed(t, e, eventID), "the duplicate charge is still marked processed")
	})

	t.Run("cancelled before success becomes REFUND_PENDING without events", func(t *testing.T) {
		orders := awaitingCheckout(t, e, 2, 30*time.Minute)
		svc := app.NewOrderService(e.store, e.clock.Now)
		_, err := svc.Act(ctx, app.ActInput{
			OrderID: orders[0].ID, Actor: domain.Actor{ID: orders[0].BuyerID, Role: domain.RoleBuyer},
			Action: domain.ActionCancel, Note: "changed my mind",
		})
		require.NoError(t, err)
		before := outboxCount(t, e)

		require.NoError(t, h.PaymentSucceeded(ctx, uid(), paymentSucceeded(orders[0].CheckoutID, uid(), orders...)))

		for _, want := range orders {
			got, err := e.store.GetOrder(ctx, want.ID)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusCancelled, got.Status)
			assert.Equal(t, domain.PaymentRefundPending, got.PaymentStatus)
		}
		assert.Equal(t, before, outboxCount(t, e), "a late capture writes no event")
	})

	t.Run("an unknown allocation is skipped", func(t *testing.T) {
		o := awaitingCheckout(t, e, 1, 30*time.Minute)[0]
		ev := paymentSucceeded(o.CheckoutID, uid(), o)
		ev.Allocations = append([]event.Allocation{{OrderID: uid(), SellerUserID: uid(), Amount: o.Total}}, ev.Allocations...)

		require.NoError(t, h.PaymentSucceeded(ctx, uid(), ev))

		got, err := e.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusPendingConfirmation, got.Status)
		assert.Equal(t, domain.PaymentPaid, got.PaymentStatus)
	})

	t.Run("an order in another status is left alone", func(t *testing.T) {
		o := awaitingCheckout(t, e, 1, 30*time.Minute)[0]
		walk(t, e, &o, domain.ActionMarkPaid, domain.SystemActor(), e.clock.Now())
		walk(t, e, &o, domain.ActionConfirm,
			domain.Actor{ID: o.SellerUserID, Role: domain.RoleSeller}, e.clock.Now())
		before, err := e.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)

		require.NoError(t, h.PaymentSucceeded(ctx, uid(), paymentSucceeded(o.CheckoutID, uid(), o)))

		got, err := e.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusConfirmed, got.Status)
		assert.Equal(t, before.Version, got.Version)
		assert.Equal(t, domain.PaymentPaid, got.PaymentStatus)
	})

	t.Run("a refund marks REFUNDED", func(t *testing.T) {
		t.Run("a cancelled paid order", func(t *testing.T) {
			o := awaitingCheckout(t, e, 1, 30*time.Minute)[0]
			walk(t, e, &o, domain.ActionMarkPaid, domain.SystemActor(), e.clock.Now())
			svc := app.NewOrderService(e.store, e.clock.Now)
			cancelled, err := svc.Act(ctx, app.ActInput{
				OrderID: o.ID, Actor: domain.Actor{ID: o.BuyerID, Role: domain.RoleBuyer},
				Action: domain.ActionCancel, Note: "changed my mind",
			})
			require.NoError(t, err)
			require.Equal(t, domain.PaymentRefundPending, cancelled.PaymentStatus)

			require.NoError(t, h.PaymentRefunded(ctx, uid(), paymentRefunded(o.CheckoutID, &o.ID)))

			got, err := e.store.GetOrder(ctx, o.ID)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusCancelled, got.Status)
			assert.Equal(t, domain.PaymentRefunded, got.PaymentStatus)
			assert.Len(t, got.History, len(cancelled.History), "a refund is not a status change")
		})

		t.Run("an out-of-order refund of a paid order", func(t *testing.T) {
			o := awaitingCheckout(t, e, 1, 30*time.Minute)[0]
			walk(t, e, &o, domain.ActionMarkPaid, domain.SystemActor(), e.clock.Now())

			require.NoError(t, h.PaymentRefunded(ctx, uid(), paymentRefunded(o.CheckoutID, &o.ID)))

			got, err := e.store.GetOrder(ctx, o.ID)
			require.NoError(t, err)
			assert.Equal(t, domain.PaymentRefunded, got.PaymentStatus)
			assert.Equal(t, domain.StatusPendingConfirmation, got.Status)
		})

		t.Run("the refund of a duplicate charge changes nothing", func(t *testing.T) {
			o := awaitingCheckout(t, e, 1, 30*time.Minute)[0]
			before, err := e.store.GetOrder(ctx, o.ID)
			require.NoError(t, err)
			eventID := uid()

			require.NoError(t, h.PaymentRefunded(ctx, eventID, paymentRefunded(o.CheckoutID, nil)))

			got, err := e.store.GetOrder(ctx, o.ID)
			require.NoError(t, err)
			assert.Equal(t, before.Version, got.Version)
			assert.Equal(t, domain.PaymentUnpaid, got.PaymentStatus)
			assert.True(t, processed(t, e, eventID))
		})
	})
}

// raceAfterRead lets the handler read the order, then cancels it as the buyer
// before handing the (now stale) read back, so the handler's write loses the
// optimistic lock deterministically and it must retry against fresh state.
type raceAfterRead struct {
	app.Store
	svc     *app.OrderService
	order   domain.Order
	raced   atomic.Bool
	reads   atomic.Int32
	cancels atomic.Int32
}

func (s *raceAfterRead) InTx(ctx context.Context, fn func(app.Tx) error) error {
	return s.Store.InTx(ctx, func(tx app.Tx) error { return fn(&raceAfterReadTx{Tx: tx, s: s}) })
}

type raceAfterReadTx struct {
	app.Tx
	s *raceAfterRead
}

func (t *raceAfterReadTx) GetOrder(ctx context.Context, id string) (domain.Order, error) {
	s := t.s
	s.reads.Add(1)
	o, err := t.Tx.GetOrder(ctx, id)
	if err != nil || id != s.order.ID || !s.raced.CompareAndSwap(false, true) {
		return o, err
	}
	if _, err := s.svc.Act(ctx, app.ActInput{
		OrderID: s.order.ID, Actor: domain.Actor{ID: s.order.BuyerID, Role: domain.RoleBuyer},
		Action: domain.ActionCancel, Note: "bought it elsewhere",
	}); err != nil {
		return domain.Order{}, err
	}
	s.cancels.Add(1)
	return o, nil // stale: still AWAITING_PAYMENT at the old version
}

// TestPaymentEventHandlerRetriesALostRace (final review M6): a buyer cancel
// that lands between the handler's read and its write makes the capture lose
// the optimistic lock; the retry sees CANCELLED and records a late capture.
func TestPaymentEventHandlerRetriesALostRace(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true})
	o := awaitingCheckout(t, e, 1, 30*time.Minute)[0]
	store := &raceAfterRead{Store: e.store, svc: app.NewOrderService(e.store, e.clock.Now), order: o}
	h := app.NewPaymentEventHandler(store, e.clock.Now, quietLogger)
	eventID := uid()

	require.NoError(t, h.PaymentSucceeded(ctx, eventID, paymentSucceeded(o.CheckoutID, uid(), o)))
	assert.Equal(t, int32(1), store.cancels.Load())
	assert.Equal(t, int32(2), store.reads.Load(), "the lost race is retried once against fresh state")

	got, err := e.store.GetOrder(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCancelled, got.Status)
	assert.Equal(t, domain.PaymentRefundPending, got.PaymentStatus)
	require.NotNil(t, got.CancelReason)
	assert.Equal(t, "bought it elsewhere", *got.CancelReason)
	assert.Equal(t, 0, countTo(got, domain.StatusPendingConfirmation), "the losing capture left no history")
	assert.True(t, processed(t, e, eventID))
}

// recordingStore records the order IDs passed to Tx.SaveTransition, in order,
// so a test can check the order in which rows are locked.
type recordingStore struct {
	app.Store
	mu    sync.Mutex
	saved []string
}

func (s *recordingStore) InTx(ctx context.Context, fn func(app.Tx) error) error {
	return s.Store.InTx(ctx, func(tx app.Tx) error { return fn(recordingTx{Tx: tx, s: s}) })
}

func (s *recordingStore) Saved() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.saved)
}

type recordingTx struct {
	app.Tx
	s *recordingStore
}

func (t recordingTx) SaveTransition(ctx context.Context, o domain.Order, prevVersion int) error {
	t.s.mu.Lock()
	t.s.saved = append(t.s.saved, o.ID)
	t.s.mu.Unlock()
	return t.Tx.SaveTransition(ctx, o, prevVersion)
}

func sortedIDs(orders []domain.Order) []string {
	ids := make([]string, 0, len(orders))
	for _, o := range orders {
		ids = append(ids, o.ID)
	}
	slices.Sort(ids)
	return ids
}

// TestPaymentEventHandlerLocksInOrderIDOrder (final review M1): allocations
// are applied in ascending order ID, whatever order the event lists them in,
// so the handler and a buyer cancel fan-out lock rows in the same order.
func TestPaymentEventHandlerLocksInOrderIDOrder(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true})
	orders := awaitingCheckout(t, e, 3, 30*time.Minute)
	want := sortedIDs(orders)
	byIDDesc := slices.Clone(orders)
	slices.SortFunc(byIDDesc, func(a, b domain.Order) int { return strings.Compare(b.ID, a.ID) })
	store := &recordingStore{Store: e.store}
	h := app.NewPaymentEventHandler(store, e.clock.Now, quietLogger)

	require.NoError(t, h.PaymentSucceeded(ctx, uid(), paymentSucceeded(orders[0].CheckoutID, uid(), byIDDesc...)))
	assert.Equal(t, want, store.Saved())
}

// TestPaymentEventHandlerSkipsAnotherCheckoutsOrder (final review M2): an
// allocation whose order belongs to a different checkout is skipped with a
// warning; the event's own orders are still applied.
func TestPaymentEventHandlerSkipsAnotherCheckoutsOrder(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true})
	log := &bytes.Buffer{}
	h := app.NewPaymentEventHandler(e.store, e.clock.Now, slog.New(slog.NewTextHandler(log, nil)))
	own := awaitingCheckout(t, e, 1, 30*time.Minute)[0]
	foreign := awaitingCheckout(t, e, 1, 30*time.Minute)[0]

	ev := paymentSucceeded(own.CheckoutID, uid(), own, foreign)
	require.NoError(t, h.PaymentSucceeded(ctx, uid(), ev))

	got, err := e.store.GetOrder(ctx, own.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPendingConfirmation, got.Status)
	assert.Equal(t, domain.PaymentPaid, got.PaymentStatus)
	got, err = e.store.GetOrder(ctx, foreign.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusAwaitingPayment, got.Status, "another checkout's order is not marked paid")
	assert.Equal(t, domain.PaymentUnpaid, got.PaymentStatus)
	assert.Empty(t, e.events(t, foreign.ID))
	assert.Contains(t, log.String(), "another checkout")
	assert.Contains(t, log.String(), foreign.ID)
}
