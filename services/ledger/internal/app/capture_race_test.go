package app_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

// TestConcurrentCapturesOfOneOrderPostOneCapture pins the interleaving where
// two payments of different checkouts (different partitions, different
// providers, so no shared account lock) allocate the same order: T2's handler
// runs while T1 has captured but not committed. T2 must wait for T1 and then
// leave its payment in suspense instead of capturing the order again.
func TestConcurrentCapturesOfOneOrderPostOneCapture(t *testing.T) {
	h := newHarness(t)
	order, seller := newID(), newID()
	c1, p1 := newID(), newID()
	a1 := domain.Allocation{OrderID: order, PaymentID: p1, CheckoutID: c1, SellerUserID: seller,
		Amount: 100000, Commission: 5000, Provider: "vnpay"}

	captured := make(chan struct{})
	allowCommit := make(chan struct{})
	t1done := make(chan error, 1)
	go func() { // T1 does what paymentSucceeded does, then holds its tx open.
		t1done <- h.store.InTx(ctx, func(tx app.Tx) error {
			pay, err := domain.Pay(p1, c1, a1.Provider, a1.Amount)
			if err != nil {
				return err
			}
			if _, err := app.Post(ctx, tx, pay); err != nil {
				return err
			}
			if err := tx.LockOrders(ctx, []uuid.UUID{order}); err != nil {
				return err
			}
			if err := tx.InsertAllocation(ctx, a1); err != nil {
				return err
			}
			capture, err := domain.Capture(p1, c1, a1.Amount, []domain.Allocation{a1})
			if err != nil {
				return err
			}
			if _, err := app.Post(ctx, tx, capture); err != nil {
				return err
			}
			close(captured)
			<-allowCommit
			return nil
		})
	}()
	<-captured

	c2 := newID()
	second, err := event.New(event.PaymentSucceeded{PaymentID: newID().String(), CheckoutID: c2.String(),
		BuyerID: newID().String(), Provider: "fake", ProviderRef: "ref-2", Amount: 100000,
		Allocations: []event.Allocation{{OrderID: order.String(), SellerUserID: seller.String(),
			Amount: 100000, CommissionAmount: 5000}}}, h.clock.Now())
	require.NoError(t, err)
	t2done := make(chan error, 1)
	go func() { t2done <- h.handle(second) }()

	// T2 is provably inside the window once it waits on a lock T1 holds.
	require.Eventually(t, func() bool {
		var waiting int
		require.NoError(t, h.db.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE NOT granted`).Scan(&waiting))
		return waiting > 0
	}, 10*time.Second, 10*time.Millisecond, "T2 never blocked on T1")
	close(allowCommit)
	require.NoError(t, <-t1done)
	require.NoError(t, <-t2done)

	var captures int
	require.NoError(t, h.db.QueryRow(ctx,
		`SELECT count(*) FROM journal_transactions WHERE kind = 'CAPTURE'`).Scan(&captures))
	assert.Equal(t, 1, captures, "one order must have exactly one capture")
	assert.Equal(t, int64(-100000), h.balance(escrow(order)), "escrow must be exactly -aᵢ")
	assert.Equal(t, int64(-100000), h.balance("checkout_suspense:"+c2.String()), "second charge waits in suspense")
	assert.True(t, h.logged("already captured"))
	assert.Zero(t, h.total())
}
