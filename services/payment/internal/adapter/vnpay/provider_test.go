package vnpay_test

import (
	"context"
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
	p := newProvider("http://unused.test", fixedNow)
	assert.Equal(t, "vnpay", p.Name())

	sess, err := p.CreateSession(bg, app.SessionInput{
		PaymentID: payID, CheckoutID: "c1", Amount: 350_000,
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

func TestCreateSessionKeepsOrderInfoASCII(t *testing.T) {
	p := newProvider("http://unused.test", fixedNow)
	sess, err := p.CreateSession(bg, app.SessionInput{
		PaymentID: payID, Amount: 1000, ExpiresAt: fixedNow.Add(time.Hour), Description: "Đơn hàng c1", ClientIP: "::1",
	})
	require.NoError(t, err)
	u, err := url.Parse(sess.RedirectURL)
	require.NoError(t, err)
	info := u.Query().Get("vnp_OrderInfo")
	for _, r := range info {
		assert.Less(t, r, rune(0x80), "vnp_OrderInfo must be ASCII: %q", info)
	}
	assert.True(t, vnpay.VerifyQuery(testSecret, u.Query()))
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

func TestQueryUnavailable(t *testing.T) {
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

func TestQueryRefundAndCancel(t *testing.T) {
	p := newProvider("http://unused.test", fixedNow)
	got, err := p.QueryRefund(bg, app.Refund{ID: "r1", ProviderRefundRef: "ref1"})
	require.NoError(t, err)
	assert.Equal(t, app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: "ref1"}, got)

	got, err = p.QueryRefund(bg, app.Refund{ID: "r1"})
	require.NoError(t, err)
	assert.Equal(t, app.RefundPending, got.State)

	cancelled, err := p.Cancel(bg, pendingPayment(1))
	require.NoError(t, err)
	assert.False(t, cancelled)
}
