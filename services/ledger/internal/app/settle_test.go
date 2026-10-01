package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/testsupport"
)

// fakeBank records transfers; fail decides the result of the n-th call (0-based).
type fakeBank struct {
	mu    sync.Mutex
	calls []uuid.UUID
	fail  func(n int) error
}

func (b *fakeBank) Transfer(_ context.Context, payoutID, _ uuid.UUID, _ int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.calls)
	b.calls = append(b.calls, payoutID)
	if b.fail != nil {
		return b.fail(n)
	}
	return nil
}

func (h *harness) settleJob(bank app.Bank) *app.SettleJob {
	return app.NewSettleJob(h.store, bank, app.SettleConfig{Interval: time.Second, Delay: time.Minute, Now: h.clock.Now, Logger: h.logger})
}

func (h *harness) payoutRow(seller uuid.UUID) (status string, reason *string, settledAt *time.Time) {
	h.t.Helper()
	require.NoError(h.t, h.db.QueryRow(ctx, `SELECT status, failure_reason, settled_at FROM payouts WHERE seller_user_id = $1`,
		seller).Scan(&status, &reason, &settledAt))
	return status, reason, settledAt
}

func TestSettleWaitsForDelayThenSettles(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	h.fund(seller, 100000)
	_, err := h.request(seller, "k1", 60000)
	require.NoError(t, err)
	job := h.settleJob(&fakeBank{})

	n, err := job.RunOnce(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "not before PAYOUT_SETTLE_DELAY")

	h.clock.Advance(61 * time.Second)
	n, err = job.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	status, reason, settledAt := h.payoutRow(seller)
	assert.Equal(t, "SETTLED", status)
	assert.Nil(t, reason)
	assert.NotNil(t, settledAt)
	assert.Zero(t, h.balance("seller_payout_pending:"+seller.String()))
	assert.Equal(t, int64(-60000), h.balance("platform_cash"))
	assert.Equal(t, int64(-40000), h.balance(wallet(seller)))
	assert.Zero(t, h.total())

	_, payloads := h.outbox(event.TopicPayoutEvents)
	require.Len(t, payloads, 2)
	testsupport.AssertEventSchema(t, "payout-settled.schema.json", payloads[1])
}

func TestSettleRejectedFailsAndReturnsFunds(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	h.fund(seller, 100000)
	_, err := h.request(seller, "k1", 60000)
	require.NoError(t, err)
	h.clock.Advance(2 * time.Minute)
	bank := &fakeBank{fail: func(int) error { return &app.TransferRejectedError{Reason: "account closed"} }}

	n, err := h.settleJob(bank).RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	status, reason, settledAt := h.payoutRow(seller)
	assert.Equal(t, "FAILED", status)
	require.NotNil(t, reason)
	assert.Equal(t, "account closed", *reason)
	assert.Nil(t, settledAt)
	assert.Equal(t, int64(-100000), h.balance(wallet(seller)))
	assert.Zero(t, h.balance("seller_payout_pending:"+seller.String()))

	_, payloads := h.outbox(event.TopicPayoutEvents)
	require.Len(t, payloads, 2)
	testsupport.AssertEventSchema(t, "payout-failed.schema.json", payloads[1])
}

func TestSettleTransientErrorRetriesTheSamePayout(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	h.fund(seller, 100000)
	_, err := h.request(seller, "k1", 60000)
	require.NoError(t, err)
	h.clock.Advance(2 * time.Minute)
	bank := &fakeBank{fail: func(n int) error {
		if n == 0 {
			return errors.New("bank timeout")
		}
		return nil
	}}
	job := h.settleJob(bank)

	_, err = job.RunOnce(ctx)
	require.Error(t, err)
	status, _, _ := h.payoutRow(seller)
	assert.Equal(t, "REQUESTED", status)

	n, err := job.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.Len(t, bank.calls, 2)
	assert.Equal(t, bank.calls[0], bank.calls[1], "the same payout id: the bank port is idempotent per payout")
	status, _, _ = h.payoutRow(seller)
	assert.Equal(t, "SETTLED", status)
}
