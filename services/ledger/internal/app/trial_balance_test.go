package app_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/testsupport"
)

func TestTrialBalanceOfWorkedExample(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("vnpay", orderSpec{amount: 230000, commission: 10000}, orderSpec{amount: 100000, commission: 3500})
	h.deliver(c.succeeded())
	h.deliver(c.completed(0))
	h.deliver(c.refunded(1))

	tb, err := h.store.TrialBalance(ctx)
	require.NoError(t, err)
	assert.True(t, tb.OK())
	assert.Equal(t, map[string]int64{"asset": 230000, "liability": -220000, "revenue": -10000}, tb.BalancesByType)
	assert.Zero(t, tb.Total)
}

func TestTrialBalanceOfEmptyLedgerIsOK(t *testing.T) {
	h := newHarness(t)
	tb, err := h.store.TrialBalance(ctx)
	require.NoError(t, err)
	assert.True(t, tb.OK())
	assert.Empty(t, tb.BalancesByType)
}

func TestTrialBalanceDetectsCorruption(t *testing.T) {
	h := newHarness(t)
	testsupport.InsertCorruptTransaction(t, h.db)
	tb, err := h.store.TrialBalance(ctx)
	require.NoError(t, err)
	assert.False(t, tb.OK())
	assert.Equal(t, int64(100), tb.Total)
	assert.Equal(t, int64(1), tb.UnbalancedTransactions)
	assert.Equal(t, int64(1), tb.LiabilitiesInDebit)
}
