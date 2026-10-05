package vnpay_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/vnpay"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

var (
	bg = context.Background()
	// 2026-09-29 08:30:05 UTC is 15:30:05 in GMT+7.
	fixedNow = time.Date(2026, 9, 29, 8, 30, 5, 0, time.UTC)
)

const payID = "0192f0c1-a2b3-7c4d-8e9f-a0b1c2d3e4f5"

var _ app.Provider = (*vnpay.Provider)(nil)

func TestCreateSessionBuildsASignedPayURL(t *testing.T) {
	// The provider clock is well past the payment's creation: vnp_CreateDate must come from CreatedAt.
	p := newProvider("http://unused.test", fixedNow.Add(90*time.Minute))
	assert.Equal(t, "vnpay", p.Name())

	sess, err := p.CreateSession(bg, app.SessionInput{
		PaymentID: payID, CheckoutID: "c1", Amount: 350_000, CreatedAt: fixedNow,
		ExpiresAt:   time.Date(2026, 9, 29, 8, 45, 0, 0, time.UTC),
		ReturnURL:   "https://gw.test/api/payments/vnpay/return",
		Description: "Thanh toan don hang c1", ClientIP: "203.0.113.7",
	})
	require.NoError(t, err)
	assert.Equal(t, "0192f0c1a2b37c4d8e9fa0b1c2d3e4f5", sess.ProviderRef)

	u, err := url.Parse(sess.RedirectURL)
	require.NoError(t, err)
	assert.Equal(t, "https://sandbox.vnpayment.vn/paymentv2/vpcpay.html", u.Scheme+"://"+u.Host+u.Path)
	assert.True(t, strings.HasSuffix(u.RawQuery, "&vnp_SecureHash="+u.Query().Get("vnp_SecureHash")), "hash is appended last")

	q := u.Query()
	assert.True(t, vnpay.VerifyQuery(testSecret, q))
	want := map[string]string{
		"vnp_Version": "2.1.0", "vnp_Command": "pay", "vnp_TmnCode": testTmn,
		"vnp_Amount": "35000000", "vnp_CurrCode": "VND", "vnp_TxnRef": "0192f0c1a2b37c4d8e9fa0b1c2d3e4f5",
		"vnp_OrderInfo": "Thanh toan don hang c1", "vnp_OrderType": "other", "vnp_Locale": "vn",
		"vnp_ReturnUrl": "https://gw.test/api/payments/vnpay/return", "vnp_IpAddr": "203.0.113.7",
		"vnp_CreateDate": "20260929153005", "vnp_ExpireDate": "20260929154500",
	}
	for k, v := range want {
		assert.Equal(t, v, q.Get(k), k)
	}
	assert.Len(t, q, len(want)+1, "no unexpected parameters")
}

func TestCreateSessionSanitizesOrderInfo(t *testing.T) {
	long := strings.Repeat("a", 300)
	cases := []struct{ in, want string }{
		{"Thanh toán ~ *đơn* hàng: c-1_x.", "Thanh ton n hng: c-1_x."},
		{"Thanh toan don hang 0192f0c1-a2b3", "Thanh toan don hang 0192f0c1-a2b3"},
		{"a~b*c/d?e&f=g+h%i", "abcdefghi"},
		{long, long[:255]},
		{"~*đ", "Thanh toan " + vnpay.TxnRef(payID)},
	}
	p := newProvider("http://unused.test", fixedNow)
	for _, tc := range cases {
		sess, err := p.CreateSession(bg, app.SessionInput{
			PaymentID: payID, Amount: 1000, CreatedAt: fixedNow, ExpiresAt: fixedNow.Add(time.Hour), Description: tc.in, ClientIP: "::1",
		})
		require.NoError(t, err)
		u, err := url.Parse(sess.RedirectURL)
		require.NoError(t, err)
		assert.Equal(t, tc.want, u.Query().Get("vnp_OrderInfo"), tc.in)
		assert.True(t, vnpay.VerifyQuery(testSecret, u.Query()))
	}
}

// Ruling (a): the pay URL's vnp_CreateDate and a later querydr's vnp_TransactionDate are the same
// second, even when the creation clock has sub-second nanos and the session is built in the next second.
func TestPayURLCreateDateMatchesQueryTransactionDate(t *testing.T) {
	clock := time.Date(2026, 9, 29, 8, 30, 5, 987_654_321, time.UTC)
	createdAt := clock.Truncate(time.Second) // what app.Creator stores and passes in SessionInput
	stub := newAPIStub(t, "querydr_pending")
	p := newProvider(stub.srv.URL, clock.Add(300*time.Millisecond)) // 08:30:06.28…

	sess, err := p.CreateSession(bg, app.SessionInput{
		PaymentID: payID, Amount: 350_000, CreatedAt: createdAt, ExpiresAt: clock.Add(time.Hour),
		Description: "Thanh toan don hang c1", ClientIP: "::1",
	})
	require.NoError(t, err)
	u, err := url.Parse(sess.RedirectURL)
	require.NoError(t, err)

	pay := pendingPayment(350_000)
	pay.CreatedAt = createdAt
	_, err = p.Query(bg, pay)
	require.NoError(t, err)
	require.Len(t, stub.calls(), 1)
	assert.Equal(t, "20260929153005", u.Query().Get("vnp_CreateDate"))
	assert.Equal(t, u.Query().Get("vnp_CreateDate"), stub.calls()[0]["vnp_TransactionDate"])
}

func pendingPayment(amount int64) domain.Payment {
	return domain.Payment{
		ID: payID, CheckoutID: "c1", Provider: "vnpay", Status: domain.StatusPending, Amount: amount,
		ProviderRef: vnpay.TxnRef(payID), ProviderTxnID: "14226112",
		CreatedAt: time.Date(2026, 9, 29, 8, 20, 0, 0, time.UTC), ExpiresAt: fixedNow.Add(10 * time.Minute),
	}
}

func TestQueryMapsTransactionStatus(t *testing.T) {
	cases := []struct {
		fixture string
		want    domain.Observed
	}{
		{"querydr_paid", domain.Observed{State: domain.ObservedSucceeded, Amount: 350_000, ProviderTxnID: "14226112"}},
		{"querydr_pending", domain.Observed{State: domain.ObservedPending}},
		{"querydr_processing", domain.Observed{State: domain.ObservedPending}},
		{"querydr_refund_sent", domain.Observed{State: domain.ObservedPending}},
		{"querydr_suspected", domain.Observed{State: domain.ObservedPending}},
		{"querydr_failed", domain.Observed{State: domain.ObservedFailed, Reason: "02"}},
		{"querydr_notfound", domain.Observed{State: domain.ObservedPending}},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			stub := newAPIStub(t, tc.fixture)
			got, err := newProvider(stub.srv.URL, fixedNow).Query(bg, pendingPayment(350_000))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			calls := stub.calls()
			require.Len(t, calls, 1)
			req := calls[0]
			assert.Equal(t, "querydr", req["vnp_Command"])
			assert.Equal(t, "2.1.0", req["vnp_Version"])
			assert.Equal(t, testTmn, req["vnp_TmnCode"])
			assert.Equal(t, vnpay.TxnRef(payID), req["vnp_TxnRef"])
			assert.Equal(t, "20260929152000", req["vnp_TransactionDate"], "the payment's creation time in GMT+7")
			assert.Equal(t, "20260929153005", req["vnp_CreateDate"])
			assert.Len(t, req["vnp_RequestId"], 32)
		})
	}
}

func TestQueryNotFoundAfterExpiryIsExpired(t *testing.T) {
	stub := newAPIStub(t, "querydr_notfound")
	p := pendingPayment(350_000)
	p.ExpiresAt = fixedNow.Add(-time.Second)
	got, err := newProvider(stub.srv.URL, fixedNow).Query(bg, p)
	require.NoError(t, err)
	assert.Equal(t, domain.Observed{State: domain.ObservedExpired}, got)
}

func TestQuerySuspectedFraudLogsAWarning(t *testing.T) {
	stub := newAPIStub(t, "querydr_suspected")
	var logs bytes.Buffer
	cfg := testConfig(stub.srv.URL, fixedNow)
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	got, err := vnpay.New(cfg).Query(bg, pendingPayment(350_000))
	require.NoError(t, err)
	assert.Equal(t, domain.ObservedPending, got.State)
	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "payment_id="+payID)
}

func TestQueryUnavailable(t *testing.T) {
	t.Run("amount not a whole number of dong", func(t *testing.T) {
		stub := newAPIStub(t, "querydr_paid_odd_amount")
		_, err := newProvider(stub.srv.URL, fixedNow).Query(bg, pendingPayment(350_000))
		assert.ErrorIs(t, err, app.ErrProviderUnavailable)
	})
	t.Run("bad response hash", func(t *testing.T) {
		stub := newAPIStub(t, "querydr_paid")
		stub.set("querydr_paid", 0, true)
		_, err := newProvider(stub.srv.URL, fixedNow).Query(bg, pendingPayment(350_000))
		assert.ErrorIs(t, err, app.ErrProviderUnavailable)
	})
	t.Run("5xx", func(t *testing.T) {
		stub := newAPIStub(t, "querydr_paid")
		stub.set("querydr_paid", 503, false)
		_, err := newProvider(stub.srv.URL, fixedNow).Query(bg, pendingPayment(350_000))
		assert.ErrorIs(t, err, app.ErrProviderUnavailable)
	})
	t.Run("429 too many requests is retryable", func(t *testing.T) {
		stub := newAPIStub(t, "querydr_paid")
		stub.set("querydr_paid", 429, false)
		_, err := newProvider(stub.srv.URL, fixedNow).Query(bg, pendingPayment(350_000))
		assert.ErrorIs(t, err, app.ErrProviderUnavailable)
	})
	t.Run("409 conflict is retryable", func(t *testing.T) {
		stub := newAPIStub(t, "querydr_paid")
		stub.set("querydr_paid", 409, false)
		_, err := newProvider(stub.srv.URL, fixedNow).Query(bg, pendingPayment(350_000))
		assert.ErrorIs(t, err, app.ErrProviderUnavailable)
	})
	t.Run("transport", func(t *testing.T) {
		_, err := newProvider("http://127.0.0.1:1", fixedNow).Query(bg, pendingPayment(350_000))
		assert.ErrorIs(t, err, app.ErrProviderUnavailable)
	})
}

func TestRefundTransactionType(t *testing.T) {
	cases := []struct {
		name     string
		amount   int64
		wantType string
	}{
		{"full", 350_000, "02"},
		{"partial", 100_000, "03"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newAPIStub(t, "refund_accepted")
			refundID := "0192f0c1-0000-7000-8000-00000000abcd"
			got, err := newProvider(stub.srv.URL, fixedNow).Refund(bg, app.RefundInput{
				RefundID: refundID, Payment: pendingPayment(350_000), Amount: tc.amount,
			})
			require.NoError(t, err)
			assert.Equal(t, app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: vnpay.TxnRef(refundID)}, got)

			calls := stub.calls()
			require.Len(t, calls, 1)
			req := calls[0]
			assert.True(t, stub.hashValid[0], "request pipe hash")
			assert.Equal(t, "refund", req["vnp_Command"])
			assert.Equal(t, tc.wantType, req["vnp_TransactionType"])
			assert.Equal(t, itoa(tc.amount*100), req["vnp_Amount"])
			assert.Equal(t, "14226112", req["vnp_TransactionNo"])
			assert.Equal(t, "20260929152000", req["vnp_TransactionDate"])
			assert.Equal(t, "system", req["vnp_CreateBy"])
			assert.Equal(t, "20260929153005", req["vnp_CreateDate"])
			assert.Equal(t, vnpay.TxnRef(refundID), req["vnp_RequestId"], "request id is stable per refund")
		})
	}
}

func TestRefundOutcomes(t *testing.T) {
	in := app.RefundInput{RefundID: "0192f0c1-0000-7000-8000-00000000abcd", Payment: pendingPayment(350_000), Amount: 350_000}

	stub := newAPIStub(t, "refund_rejected")
	got, err := newProvider(stub.srv.URL, fixedNow).Refund(bg, in)
	require.NoError(t, err)
	assert.Equal(t, app.RefundObserved{State: app.RefundFailed, Reason: "vnpay_95"}, got)

	stub.set("refund_declined", 0, false)
	got, err = newProvider(stub.srv.URL, fixedNow).Refund(bg, in)
	require.NoError(t, err)
	assert.Equal(t, app.RefundObserved{State: app.RefundFailed, Reason: "vnpay_status_09"}, got)

	stub.set("refund_accepted", 0, true)
	_, err = newProvider(stub.srv.URL, fixedNow).Refund(bg, in)
	assert.ErrorIs(t, err, app.ErrProviderUnavailable)

	stub.set("refund_accepted", 502, false)
	_, err = newProvider(stub.srv.URL, fixedNow).Refund(bg, in)
	assert.ErrorIs(t, err, app.ErrProviderUnavailable)
}

// paymentsByID is the provider's payment reader in tests.
type paymentsByID map[string]domain.Payment

func (m paymentsByID) GetPayment(_ context.Context, id string) (domain.Payment, error) {
	p, ok := m[id]
	if !ok {
		return domain.Payment{}, app.ErrNotFound
	}
	return p, nil
}

// Ruling (b): RC 94 ("already requested") is Pending with the request id as ref, and QueryRefund
// resolves it through querydr to a terminal state.
func TestRefundAlreadyRequestedResolvesThroughQuerydr(t *testing.T) {
	refundID := "0192f0c1-0000-7000-8000-00000000abcd"
	pay := pendingPayment(350_000)
	pay.Status = domain.StatusSucceeded
	stub := newAPIStub(t, "refund_already_requested")
	cfg := testConfig(stub.srv.URL, fixedNow)
	cfg.Payments = paymentsByID{payID: pay}
	p := vnpay.New(cfg)

	got, err := p.Refund(bg, app.RefundInput{RefundID: refundID, Payment: pay, Amount: 350_000})
	require.NoError(t, err)
	assert.Equal(t, app.RefundObserved{State: app.RefundPending, ProviderRefundRef: vnpay.TxnRef(refundID)}, got)

	refund := app.Refund{ID: refundID, PaymentID: payID, Provider: "vnpay", Amount: 350_000, ProviderRefundRef: got.ProviderRefundRef}
	cases := []struct {
		fixture string
		want    app.RefundObserved
	}{
		{"querydr_refunded", app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: vnpay.TxnRef(refundID)}},
		{"querydr_refund_sent", app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: vnpay.TxnRef(refundID)}},
		{"querydr_refund_declined", app.RefundObserved{State: app.RefundFailed, Reason: "vnpay_status_09"}},
		{"querydr_paid", app.RefundObserved{State: app.RefundFailed, Reason: "vnpay_refund_not_found"}},
		{"querydr_notfound", app.RefundObserved{State: app.RefundFailed, Reason: "vnpay_91"}},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			stub.set(tc.fixture, 0, false)
			n := len(stub.calls())
			got, err := p.QueryRefund(bg, refund)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			calls := stub.calls()
			require.Len(t, calls, n+1)
			assert.Equal(t, "querydr", calls[n]["vnp_Command"])
			assert.Equal(t, vnpay.TxnRef(payID), calls[n]["vnp_TxnRef"])
			assert.Equal(t, "20260929152000", calls[n]["vnp_TransactionDate"])
		})
	}

	stub.set("querydr_refunded", 0, true)
	_, err = p.QueryRefund(bg, refund)
	assert.ErrorIs(t, err, app.ErrProviderUnavailable)
}

// A refund the provider already accepted (its reference is stored) is only ever polled, so a
// throttled VNPay keeps it PENDING: no attempt is spent and no RefundFailed is emitted, however many
// runs pass. This is the adapter side of review #3 — a 429 must read as unavailable, never as a
// failure — and the worker side is covered by TestRefundWorkerAccepted.
func TestRefundWorkerKeepsAcceptedRefundPendingOn429(t *testing.T) {
	stub := newAPIStub(t, "querydr_paid")
	refundID := "0192f0c1-0000-7000-8000-00000000abcd"
	ref := vnpay.TxnRef(refundID)
	refund := app.Refund{ID: refundID, PaymentID: payID, Provider: "vnpay", Amount: 350_000, ProviderRefundRef: ref}
	pay := pendingPayment(350_000)
	cfg := testConfig(stub.srv.URL, fixedNow)
	cfg.Payments = paymentsByID{payID: pay}
	p := vnpay.New(cfg)

	for _, tc := range []struct {
		name   string
		status int
		obs    app.RefundObserved
	}{
		{"429 throttled", http.StatusTooManyRequests, app.RefundObserved{}},
		{"409 conflict", http.StatusConflict, app.RefundObserved{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub.set("querydr_paid", tc.status, false)
			got, err := p.QueryRefund(bg, refund)
			assert.ErrorIs(t, err, app.ErrProviderUnavailable, tc.name)
			assert.Equal(t, tc.obs, got, "no state to settle: the worker reschedules by age")
		})
	}
}

func TestQueryRefundAndCancel(t *testing.T) {
	p := newProvider("http://unused.test", fixedNow)
	got, err := p.QueryRefund(bg, app.Refund{ID: "r1", PaymentID: payID})
	require.NoError(t, err)
	assert.Equal(t, app.RefundPending, got.State, "nothing was requested yet")

	_, err = p.QueryRefund(bg, app.Refund{ID: "r1", PaymentID: payID, ProviderRefundRef: "ref1"})
	assert.Error(t, err, "querydr needs the payment reader")

	cancelled, err := p.Cancel(bg, pendingPayment(1))
	require.NoError(t, err)
	assert.False(t, cancelled)
}
