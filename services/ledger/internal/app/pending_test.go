package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

func (h *harness) pendingJob() *app.PendingJob {
	return app.NewPendingJob(h.store, h.events, app.PendingConfig{Interval: time.Second, Now: h.clock.Now, Logger: h.logger})
}

// captureDirectly writes the allocation and CAPTURE without consuming pending
// postings, as when the capture committed while the pending row was locked.
func (h *harness) captureDirectly(c checkout) {
	h.t.Helper()
	o := c.orders[0]
	a := domain.Allocation{OrderID: o.id, PaymentID: c.payment, CheckoutID: c.id, SellerUserID: o.seller,
		Amount: o.amount, Commission: o.commission, Provider: c.provider}
	require.NoError(h.t, h.store.InTx(ctx, func(tx app.Tx) error {
		pay, _ := domain.Pay(c.payment, c.id, c.provider, o.amount)
		capture, _ := domain.Capture(c.payment, c.id, o.amount, []domain.Allocation{a})
		if _, err := app.Post(ctx, tx, pay); err != nil {
			return err
		}
		if err := tx.InsertAllocation(ctx, a); err != nil {
			return err
		}
		_, err := app.Post(ctx, tx, capture)
		return err
	}))
}

func TestPendingJobReleasesOnceAllocationExists(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000})
	h.deliver(c.completed(0))
	h.captureDirectly(c)

	n, err := h.pendingJob().RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Zero(t, h.count("pending_postings"))
	assert.Equal(t, int64(-95000), h.balance(wallet(c.orders[0].seller)))
}

func TestPendingJobTouchesAndAlertsWhenStale(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000})
	h.deliver(c.completed(0))
	job := h.pendingJob()

	n, err := job.RunOnce(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.False(t, h.logged("still waiting for its capture"))

	h.clock.Advance(2 * time.Hour)
	_, err = job.RunOnce(ctx)
	require.NoError(t, err)
	var attempts int
	require.NoError(t, h.db.QueryRow(ctx, `SELECT attempts FROM pending_postings`).Scan(&attempts))
	assert.Equal(t, 2, attempts)
	assert.True(t, h.logged("still waiting for its capture"))
}

func TestPendingJobClaimsUntouchedRowsFirst(t *testing.T) {
	h := newHarness(t)
	a := newCheckout("fake", orderSpec{amount: 100000, commission: 5000})
	b := newCheckout("fake", orderSpec{amount: 60000, commission: 3000})
	h.deliver(a.completed(0))
	h.clock.Advance(time.Second)
	h.deliver(b.completed(0))
	job := app.NewPendingJob(h.store, h.events, app.PendingConfig{Interval: time.Second, BatchSize: 1, Now: h.clock.Now, Logger: h.logger})

	n, err := job.RunOnce(ctx) // touches A only
	require.NoError(t, err)
	assert.Zero(t, n)
	h.captureDirectly(b)

	n, err = job.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, int64(-57000), h.balance(wallet(b.orders[0].seller)))
	assert.Equal(t, 1, h.count("pending_postings"), "A is still waiting")
}
