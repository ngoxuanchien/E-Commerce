package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

func id() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// ledger applies journals to a code → Σ map, like the database does.
type ledger map[string]int64

func (l ledger) apply(t *testing.T, j domain.Journal, err error) {
	t.Helper()
	require.NoError(t, err)
	require.NoError(t, j.Validate())
	for _, line := range j.Lines {
		l[line.Account.Code] += line.Amount
	}
}

func (l ledger) total() int64 {
	var s int64
	for _, v := range l {
		s += v
	}
	return s
}

// TestWorkedExample is spec §3's worked example.
func TestWorkedExample(t *testing.T) {
	c, p, x, y, s1, s2, r := id(), id(), id(), id(), id(), id(), id()
	ax := domain.Allocation{OrderID: x, PaymentID: p, CheckoutID: c, SellerUserID: s1, Amount: 230000, Commission: 10000, Provider: "vnpay"}
	ay := domain.Allocation{OrderID: y, PaymentID: p, CheckoutID: c, SellerUserID: s2, Amount: 100000, Commission: 3500, Provider: "vnpay"}

	l := ledger{}
	j, err := domain.Pay(p, c, "vnpay", 330000)
	l.apply(t, j, err)
	j, err = domain.Capture(p, c, 330000, []domain.Allocation{ax, ay})
	l.apply(t, j, err)
	j, err = domain.Release(ax)
	l.apply(t, j, err)
	j, err = domain.Refund(r, ay)
	l.apply(t, j, err)

	assert.Equal(t, int64(230000), l["psp_clearing:vnpay"])
	assert.Equal(t, int64(-220000), l["seller_wallet:"+s1.String()])
	assert.Equal(t, int64(220000), domain.Shown(l["seller_wallet:"+s1.String()]))
	assert.Equal(t, int64(-10000), l["platform_commission_revenue"])
	assert.Zero(t, l["checkout_suspense:"+c.String()])
	assert.Zero(t, l["escrow:"+x.String()])
	assert.Zero(t, l["escrow:"+y.String()])
	assert.Zero(t, l.total())
}

func TestKeysKindsAndRefs(t *testing.T) {
	c, p, o, s, r := id(), id(), id(), id(), id()
	a := domain.Allocation{OrderID: o, PaymentID: p, CheckoutID: c, SellerUserID: s, Amount: 100, Commission: 5, Provider: "fake"}
	po := domain.Payout{ID: id(), SellerUserID: s, Amount: 50}
	for _, tc := range []struct {
		build func() (domain.Journal, error)
		key   string
		kind  domain.Kind
	}{
		{func() (domain.Journal, error) { return domain.Pay(p, c, "fake", 100) }, "pay:" + p.String(), domain.KindPay},
		{func() (domain.Journal, error) { return domain.Capture(p, c, 100, []domain.Allocation{a}) }, "capture:" + p.String(), domain.KindCapture},
		{func() (domain.Journal, error) { return domain.Release(a) }, "release:" + o.String(), domain.KindRelease},
		{func() (domain.Journal, error) { return domain.Refund(r, a) }, "refund:" + r.String(), domain.KindRefund},
		{func() (domain.Journal, error) { return domain.DuplicateRefund(r, p, c, "fake", 100) }, "refund:" + r.String(), domain.KindDuplicateRefund},
		{func() (domain.Journal, error) { return domain.PayoutRequest(po) }, "payout:" + po.ID.String() + ":request", domain.KindPayoutRequest},
		{func() (domain.Journal, error) { return domain.PayoutSettle(po) }, "payout:" + po.ID.String() + ":settle", domain.KindPayoutSettle},
		{func() (domain.Journal, error) { return domain.PayoutFail(po) }, "payout:" + po.ID.String() + ":fail", domain.KindPayoutFail},
	} {
		j, err := tc.build()
		require.NoError(t, err, tc.key)
		assert.Equal(t, tc.key, j.Key)
		assert.Equal(t, tc.kind, j.Kind)
		assert.NoError(t, j.Validate(), tc.key)
	}
	j, _ := domain.Release(a)
	assert.Equal(t, &o, j.Refs.OrderID)
	assert.Equal(t, &p, j.Refs.PaymentID)
	assert.Equal(t, &c, j.Refs.CheckoutID)
	j, _ = domain.PayoutRequest(po)
	assert.Equal(t, &po.ID, j.Refs.PayoutID)
}

func TestReleaseWithZeroCommissionOmitsCommissionLine(t *testing.T) {
	a := domain.Allocation{OrderID: id(), PaymentID: id(), CheckoutID: id(), SellerUserID: id(), Amount: 100, Commission: 0, Provider: "stripe"}
	j, err := domain.Release(a)
	require.NoError(t, err)
	require.Len(t, j.Lines, 2)
	for _, l := range j.Lines {
		assert.NotEqual(t, "platform_commission_revenue", l.Account.Code)
	}
}

func TestBuildersRejectInvalidInput(t *testing.T) {
	c, p, o, s := id(), id(), id(), id()
	ok := domain.Allocation{OrderID: o, PaymentID: p, CheckoutID: c, SellerUserID: s, Amount: 100, Commission: 5, Provider: "fake"}
	with := func(f func(*domain.Allocation)) domain.Allocation { a := ok; f(&a); return a }
	cases := map[string]func() (domain.Journal, error){
		"pay zero":              func() (domain.Journal, error) { return domain.Pay(p, c, "fake", 0) },
		"pay negative":          func() (domain.Journal, error) { return domain.Pay(p, c, "fake", -1) },
		"pay unknown provider":  func() (domain.Journal, error) { return domain.Pay(p, c, "paypal", 100) },
		"capture empty":         func() (domain.Journal, error) { return domain.Capture(p, c, 100, nil) },
		"capture exceeds":       func() (domain.Journal, error) { return domain.Capture(p, c, 99, []domain.Allocation{ok}) },
		"capture dup order":     func() (domain.Journal, error) { return domain.Capture(p, c, 1000, []domain.Allocation{ok, ok}) },
		"capture other payment": func() (domain.Journal, error) { return domain.Capture(id(), c, 100, []domain.Allocation{ok}) },
		"commission = amount":   func() (domain.Journal, error) { return domain.Release(with(func(a *domain.Allocation) { a.Commission = 100 })) },
		"negative commission":   func() (domain.Journal, error) { return domain.Release(with(func(a *domain.Allocation) { a.Commission = -1 })) },
		"alloc zero amount":     func() (domain.Journal, error) { return domain.Refund(id(), with(func(a *domain.Allocation) { a.Amount = 0 })) },
		"alloc bad provider":    func() (domain.Journal, error) { return domain.Refund(id(), with(func(a *domain.Allocation) { a.Provider = "" })) },
		"dup refund zero":       func() (domain.Journal, error) { return domain.DuplicateRefund(id(), p, c, "fake", 0) },
		"payout zero":           func() (domain.Journal, error) { return domain.PayoutRequest(domain.Payout{ID: id(), SellerUserID: s}) },
	}
	for name, build := range cases {
		_, err := build()
		assert.ErrorIs(t, err, domain.ErrInvalidPosting, name)
	}
	_, err := domain.Capture(p, c, 99, []domain.Allocation{ok})
	assert.ErrorIs(t, err, domain.ErrAllocationsExceedPayment)
}

func TestShown(t *testing.T) {
	assert.Equal(t, int64(220000), domain.Shown(-220000))
	assert.Equal(t, int64(0), domain.Shown(0))
}

func TestParsePayoutStatus(t *testing.T) {
	for _, s := range []string{"REQUESTED", "SETTLED", "FAILED"} {
		got, ok := domain.ParsePayoutStatus(s)
		assert.True(t, ok)
		assert.Equal(t, domain.PayoutStatus(s), got)
	}
	_, ok := domain.ParsePayoutStatus("requested")
	assert.False(t, ok)
}
