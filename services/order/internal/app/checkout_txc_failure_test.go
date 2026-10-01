package app_test

import (
	"context"
	"errors"
	"net/http"
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
	failKeyRead  atomic.Bool // reading the key back fails (the commit ack was lost and the row cannot be re-read)
}

func (s *faultyStore) IdempotencyKey(ctx context.Context, userID, key string) (app.IdempotencyRecord, bool, error) {
	if s.failKeyRead.CompareAndSwap(true, false) {
		return app.IdempotencyRecord{}, false, errInjected
	}
	return s.Store.IdempotencyKey(ctx, userID, key)
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

	t.Run("a Tx C whose commit ack is lost and whose key cannot be re-read keeps the key", func(t *testing.T) {
		// The harshest ambiguous COMMIT: the server committed, the client saw
		// an error, and the row cannot be read back either, so the run cannot
		// tell whether its response landed. Releasing the key here would let
		// the retry check out a second time.
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Ack lost"), 10_000, 5)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		store.loseAck.Store(true)
		store.failKeyRead.Store(true)

		_, err := svc.Checkout(ctx, checkoutInput(buyer, "ack-lost"), (&recorder{}).encode)
		require.ErrorIs(t, err, errInjected, "the checkout reports the lost commit")

		rec := &recorder{}
		again, err := svc.Checkout(ctx, checkoutInput(buyer, "ack-lost"), rec.encode)
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, again.Status, "the retry replays the stored 201")
		ids := createdOrderIDs(t, again)
		require.Len(t, ids, 1, "the retry returns the order the first run created")
		orders := buyerOrders(t, e, buyer)
		require.Len(t, orders, 1, "no duplicate order")
		assert.Equal(t, ids[0], orders[0].ID, "the replayed order is the one already stored")
		assert.Equal(t, domain.StatusPendingConfirmation, orders[0].Status)
	})
}
