package app_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

var errInjected = errors.New("injected store failure")

// faultyStore wraps a Store to break Tx C, the first transaction that stores
// an idempotency response.
type faultyStore struct {
	app.Store
	failComplete atomic.Bool // CompleteIdempotencyKey fails (Tx C rolls back)
	loseAck      atomic.Bool // the tx commits but InTx reports an error
}

func (s *faultyStore) InTx(ctx context.Context, fn func(app.Tx) error) error {
	completed := false
	err := s.Store.InTx(ctx, func(tx app.Tx) error {
		return fn(&faultyTx{Tx: tx, store: s, completed: &completed})
	})
	if err == nil && completed && s.loseAck.CompareAndSwap(true, false) {
		return errInjected
	}
	return err
}

type faultyTx struct {
	app.Tx
	store     *faultyStore
	completed *bool
}

func (t *faultyTx) CompleteIdempotencyKey(ctx context.Context, userID, key string, claimedAt time.Time, resp app.Response) error {
	if t.store.failComplete.CompareAndSwap(true, false) {
		return errInjected
	}
	*t.completed = true
	return t.Tx.CompleteIdempotencyKey(ctx, userID, key, claimedAt, resp)
}

func TestCheckoutTxCFailure(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true, redis: true})
	store := &faultyStore{Store: e.store}
	svc := app.NewCheckoutService(store, e.carts, e.catalog, app.CheckoutConfig{ShippingFee: 30_000, Now: e.clock.Now})

	t.Run("a failed Tx C releases stock and cancels, so the retry succeeds", func(t *testing.T) {
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Last units"), 10_000, 2)
		e.cartSet(t, buyer, s.GetSkuId(), 2) // exactly the remaining stock
		store.failComplete.Store(true)

		_, err := svc.Checkout(ctx, checkoutInput(buyer, "txc"), (&recorder{}).encode)
		require.ErrorIs(t, err, errInjected)
		orders := buyerOrders(t, e, buyer)
		require.Len(t, orders, 1)
		assert.Equal(t, domain.StatusCancelled, orders[0].Status)
		assert.Equal(t, domain.CancelReserveFailed, *orders[0].CancelReason)
		assert.False(t, e.fake.Reserved(orders[0].ID))
		assert.Equal(t, int32(2), e.fake.Stock(s.GetSkuId()), "stock is back immediately, not after the sweeper")

		rec := &recorder{}
		resp, err := svc.Checkout(ctx, checkoutInput(buyer, "txc"), rec.encode)
		require.NoError(t, err)
		assert.NoError(t, rec.lastErr(), "the retry must not see a false OUT_OF_STOCK")
		assert.Len(t, createdOrderIDs(t, resp), 1)
	})

	t.Run("a Tx C whose commit ack is lost returns the stored response", func(t *testing.T) {
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Ack"), 10_000, 5)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		store.loseAck.Store(true)

		resp, err := svc.Checkout(ctx, checkoutInput(buyer, "ack"), (&recorder{}).encode)
		require.NoError(t, err)
		ids := createdOrderIDs(t, resp)
		require.Len(t, ids, 1)
		o, err := e.store.GetOrder(ctx, ids[0])
		require.NoError(t, err)
		assert.Equal(t, domain.StatusPendingConfirmation, o.Status)
		assert.True(t, e.fake.Reserved(o.ID))

		again, err := svc.Checkout(ctx, checkoutInput(buyer, "ack"), (&recorder{}).encode)
		require.NoError(t, err)
		assert.JSONEq(t, string(resp.Body), string(again.Body), "the key was kept: no duplicate orders")
		assert.Len(t, buyerOrders(t, e, buyer), 1)
		lines, err := e.carts.Lines(ctx, buyer)
		require.NoError(t, err)
		assert.Empty(t, lines)
	})
}

// commitAtDeadline lets Tx C commit, then holds the reply until the forward
// context's deadline has fired, as if the commit landed right at the deadline.
type commitAtDeadline struct{ app.Store }

func (s commitAtDeadline) InTx(ctx context.Context, fn func(app.Tx) error) error {
	completed := false
	err := s.Store.InTx(ctx, func(tx app.Tx) error {
		return fn(&faultyTx{Tx: tx, store: &faultyStore{}, completed: &completed})
	})
	if err == nil && completed {
		<-ctx.Done()
	}
	return err
}

// A Tx C that commits just before the forward deadline still clears the
// purchased cart lines: the cart is cleaned up under its own context (T10).
func TestCheckoutClearsCartAfterACommitAtTheDeadline(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true, redis: true})
	svc := app.NewCheckoutService(commitAtDeadline{e.store}, e.carts, e.catalog, app.CheckoutConfig{
		ShippingFee: 30_000, Now: e.clock.Now, Deadline: 2 * time.Second,
	})
	buyer := uid()
	s := e.fake.AddSku(catalogfake.NewShop("Deadline cart"), 10_000, 5)
	e.cartSet(t, buyer, s.GetSkuId(), 1)

	resp, err := svc.Checkout(ctx, checkoutInput(buyer, "deadline-cart"), (&recorder{}).encode)
	require.NoError(t, err)
	assert.Len(t, createdOrderIDs(t, resp), 1)
	lines, err := e.carts.Lines(ctx, buyer)
	require.NoError(t, err)
	assert.Empty(t, lines, "purchased lines leave the cart")
}
