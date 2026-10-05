package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

func TestWalletOfUnknownSellerIsZero(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	w, err := app.NewWalletService(h.store).Wallet(ctx, seller)
	require.NoError(t, err)
	assert.Equal(t, app.Wallet{}, w)

	entries, err := app.NewWalletService(h.store).Entries(ctx, seller, app.ListInput{Limit: 20})
	require.NoError(t, err)
	assert.NotNil(t, entries.Items)
	assert.Empty(t, entries.Items)
	assert.Nil(t, entries.NextCursor)

	payouts, err := h.payouts().List(ctx, seller, nil, app.ListInput{Limit: 20})
	require.NoError(t, err)
	assert.NotNil(t, payouts.Items)
	assert.Empty(t, payouts.Items)
}

func TestWalletFiguresEqualDerivedSums(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000, seller: seller}, orderSpec{amount: 50000, commission: 2500, seller: seller})
	h.deliver(c.succeeded())
	h.deliver(c.completed(0))                                            // +95000 available; 50000 stays in escrow
	h.deliver(newCheckout("fake", orderSpec{amount: 70000}).succeeded()) // another seller's escrow
	_, err := h.request(seller, "k1", 60000)
	require.NoError(t, err)

	w, err := app.NewWalletService(h.store).Wallet(ctx, seller)
	require.NoError(t, err)
	assert.Equal(t, app.Wallet{Available: 35000, PendingPayout: 60000, InEscrow: 50000}, w)
	assert.Equal(t, w.Available, -h.balance(wallet(seller)))
}

func TestWalletEntriesNewestFirstWithSellerSigns(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000, seller: seller})
	h.deliver(c.succeeded())
	h.deliver(c.completed(0))
	_, err := h.request(seller, "k1", 60000)
	require.NoError(t, err)
	h.clock.Advance(2 * time.Minute)
	_, err = h.settleJob(&fakeBank{fail: func(int) error { return &app.TransferRejectedError{Reason: "closed"} }}).RunOnce(ctx)
	require.NoError(t, err)

	svc := app.NewWalletService(h.store)
	first, err := svc.Entries(ctx, seller, app.ListInput{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	require.NotNil(t, first.NextCursor)
	assert.Equal(t, domain.KindPayoutFail, first.Items[0].Kind)
	assert.Equal(t, int64(60000), first.Items[0].Amount)
	assert.NotNil(t, first.Items[0].PayoutID)
	assert.Equal(t, domain.KindPayoutRequest, first.Items[1].Kind)
	assert.Equal(t, int64(-60000), first.Items[1].Amount)

	second, err := svc.Entries(ctx, seller, app.ListInput{Limit: 2, Cursor: *first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	assert.Nil(t, second.NextCursor)
	assert.Equal(t, domain.KindRelease, second.Items[0].Kind)
	assert.Equal(t, int64(95000), second.Items[0].Amount)
	require.NotNil(t, second.Items[0].OrderID)
	assert.Equal(t, c.orders[0].id, *second.Items[0].OrderID)
	assert.Nil(t, second.Items[0].PayoutID)
}

func TestWalletEntriesRejectBadCursor(t *testing.T) {
	h := newHarness(t)
	_, err := app.NewWalletService(h.store).Entries(ctx, newID(), app.ListInput{Limit: 20, Cursor: "garbage"})
	assert.ErrorIs(t, err, app.ErrInvalidCursor)
}

func TestListPayoutsFiltersByStatusAndPages(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	h.fund(seller, 300000)
	for _, k := range []string{"k1", "k2", "k3"} {
		_, err := h.request(seller, k, 60000)
		require.NoError(t, err)
		h.clock.Advance(time.Second)
	}
	h.clock.Advance(2 * time.Minute)
	job := app.NewSettleJob(h.store, &fakeBank{}, app.SettleConfig{Interval: time.Second, Delay: time.Minute, BatchSize: 1, Now: h.clock.Now, Logger: h.logger})
	_, err := job.RunOnce(ctx) // settles only the oldest payout
	require.NoError(t, err)

	svc := h.payouts()
	all, err := svc.List(ctx, seller, nil, app.ListInput{Limit: 2})
	require.NoError(t, err)
	require.Len(t, all.Items, 2)
	require.NotNil(t, all.NextCursor)
	assert.True(t, all.Items[0].RequestedAt.After(all.Items[1].RequestedAt), "newest first")
	rest, err := svc.List(ctx, seller, nil, app.ListInput{Limit: 2, Cursor: *all.NextCursor})
	require.NoError(t, err)
	assert.Len(t, rest.Items, 1)

	settled := domain.PayoutSettled
	only, err := svc.List(ctx, seller, &settled, app.ListInput{Limit: 20})
	require.NoError(t, err)
	require.Len(t, only.Items, 1)
	assert.Equal(t, domain.PayoutSettled, only.Items[0].Status)
	assert.NotNil(t, only.Items[0].SettledAt)
}
