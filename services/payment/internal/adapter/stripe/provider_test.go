package stripe_test

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/stripe"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

var testNow = time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)

const (
	payID      = "0192f0c1-a2b3-7c4d-8e9f-a0b1c2d3e4f5"
	checkoutID = "0192f0c1-0000-7000-8000-000000000001"
)

func newProvider(t *testing.T) (*stripe.Provider, *apiStub) {
	t.Helper()
	api := newAPIStub(t)
	return stripe.New(testConfig(api.srv.URL, testNow)), api
}

func sessionInput(amount int64, expires time.Time) app.SessionInput {
	return app.SessionInput{PaymentID: payID, CheckoutID: checkoutID, Amount: amount, ExpiresAt: expires,
		Description: "ignored", ClientIP: "203.0.113.7", CreatedAt: testNow}
}

func stripePayment(ref string) domain.Payment {
	return domain.Payment{ID: payID, CheckoutID: checkoutID, Method: "STRIPE", Provider: "stripe",
		Status: domain.StatusPending, Amount: 350_000, ProviderRef: ref, ExpiresAt: testNow.Add(15 * time.Minute), CreatedAt: testNow}
}

func TestName(t *testing.T) {
	p, _ := newProvider(t)
	assert.Equal(t, "stripe", p.Name())
}

func TestCreateSessionParams(t *testing.T) {
	p, api := newProvider(t)
	api.set(opCreateSession, reply{fixture: "session_open"})

	s, err := p.CreateSession(bg, sessionInput(350_000, testNow.Add(2*time.Hour)))
	require.NoError(t, err)
	assert.Equal(t, testSessionID, s.ProviderRef)
	assert.Equal(t, "https://checkout.stripe.com/c/pay/cs_test_a1B2c3D4e5F6g7H8i9J0#fidkdWxOYHwnPyd1blpxYHZxWjA0", s.RedirectURL)

	calls := api.calls(opCreateSession)
	require.Len(t, calls, 1)
	f, h := calls[0].form, calls[0].header
	assert.Equal(t, payID, h.Get("Idempotency-Key"))
	assert.Equal(t, "Bearer "+testSecretKey, h.Get("Authorization"))
	returnURL := "https://gw.test/api/payments/stripe/return?session_id={CHECKOUT_SESSION_ID}"
	for k, want := range map[string]string{
		"mode":                                          "payment",
		"line_items[0][quantity]":                       "1",
		"line_items[0][price_data][currency]":           "vnd",
		"line_items[0][price_data][unit_amount]":        "350000", // zero-decimal: not ×100
		"line_items[0][price_data][product_data][name]": "Đơn hàng " + checkoutID,
		"client_reference_id":                           payID,
		"metadata[payment_id]":                          payID,
		"metadata[checkout_id]":                         checkoutID,
		"success_url":                                   returnURL,
		"cancel_url":                                    returnURL,
		"expires_at":                                    strconv.FormatInt(testNow.Add(2*time.Hour).Unix(), 10),
	} {
		assert.Equal(t, want, f.Get(k), k)
	}
}

// Stripe accepts expires_at from 30 minutes after creation; a shorter payment deadline is clamped
// (with a one-minute margin for clock skew and latency) and enforced later by Cancel.
func TestCreateSessionClampsExpiry(t *testing.T) {
	p, api := newProvider(t)
	api.set(opCreateSession, reply{fixture: "session_open"})

	_, err := p.CreateSession(bg, sessionInput(350_000, testNow.Add(10*time.Minute)))
	require.NoError(t, err)
	calls := api.calls(opCreateSession)
	require.Len(t, calls, 1)
	assert.Equal(t, strconv.FormatInt(testNow.Add(31*time.Minute).Unix(), 10), calls[0].form.Get("expires_at"))
}

func TestCreateSessionErrors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		fixture     string
		unavailable bool
	}{
		{"500", http.StatusInternalServerError, "error_api", true},
		{"503", http.StatusServiceUnavailable, "error_api", true},
		{"429", http.StatusTooManyRequests, "error_api", true},
		{"409", http.StatusConflict, "error_idempotency_in_progress", true},
		{"400", http.StatusBadRequest, "error_session_not_open", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, api := newProvider(t)
			api.set(opCreateSession, reply{fixture: tc.fixture, status: tc.status})
			_, err := p.CreateSession(bg, sessionInput(350_000, testNow.Add(time.Hour)))
			require.Error(t, err)
			assert.Equal(t, tc.unavailable, errors.Is(err, app.ErrProviderUnavailable), err)
		})
	}

	t.Run("transport", func(t *testing.T) {
		cfg := testConfig("http://127.0.0.1:1", testNow)
		zero := int64(0)
		cfg.MaxNetworkRetries = &zero
		_, err := stripe.New(cfg).CreateSession(bg, sessionInput(350_000, testNow.Add(time.Hour)))
		require.ErrorIs(t, err, app.ErrProviderUnavailable)
	})
}

func TestQueryMapsSessionStates(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		want    domain.Observed
	}{
		{"session_open", domain.Observed{State: domain.ObservedPending}},
		{"session_complete_paid", domain.Observed{State: domain.ObservedSucceeded, Amount: 350_000, ProviderTxnID: testIntentID}},
		{"session_complete_unpaid", domain.Observed{State: domain.ObservedPending}},                            // async payment settling (PI processing)
		{"session_async_failed", domain.Observed{State: domain.ObservedFailed, Reason: "stripe_async_failed"}}, // PI requires_payment_method
		{"session_expired", domain.Observed{State: domain.ObservedExpired}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			p, api := newProvider(t)
			api.set(opRetrieveSession, reply{fixture: tc.fixture})
			got, err := p.Query(bg, stripePayment(testSessionID))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			calls := api.calls(opRetrieveSession)
			require.Len(t, calls, 1)
			assert.Equal(t, "/v1/checkout/sessions/"+testSessionID, calls[0].path)
			assert.Equal(t, "payment_intent", calls[0].form.Get("expand[0]"))
		})
	}
}

// Only a complete+unpaid session consults the PaymentIntent: requires_payment_method and canceled mean
// the async payment failed; processing or anything else is still settling. An open session stays
// Pending even with a declined attempt, because the buyer can retry on the hosted page.
func TestQueryAsyncPaymentIntentStates(t *testing.T) {
	failed := domain.Observed{State: domain.ObservedFailed, Reason: "stripe_async_failed"}
	pending := domain.Observed{State: domain.ObservedPending}
	for _, tc := range []struct {
		name, fixture string
		intent        any // a PaymentIntent fixture name (inlined by the stub) or an inline object
		want          domain.Observed
	}{
		{"requires_payment_method", "session_async_failed", "payment_intent_requires_payment_method", failed},
		{"canceled", "session_async_failed", map[string]any{"id": testIntentID, "object": "payment_intent", "status": "canceled"}, failed},
		{"processing", "session_complete_unpaid", "payment_intent_processing", pending},
		{"requires_action", "session_complete_unpaid", map[string]any{"id": testIntentID, "object": "payment_intent", "status": "requires_action"}, pending},
		{"open with a declined attempt", "session_open", "payment_intent_requires_payment_method", pending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, api := newProvider(t)
			r := reply{fixture: tc.fixture}
			if name, ok := tc.intent.(string); ok {
				r.override, r.intent = map[string]any{"payment_intent": testIntentID}, name
			} else {
				r.override = map[string]any{"payment_intent": tc.intent}
			}
			api.set(opRetrieveSession, r)
			got, err := p.Query(bg, stripePayment(testSessionID))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestQueryReportsTheProviderAmount(t *testing.T) {
	p, api := newProvider(t)
	api.set(opRetrieveSession, reply{fixture: "session_complete_paid", override: map[string]any{"amount_total": 349_999}})
	got, err := p.Query(bg, stripePayment(testSessionID))
	require.NoError(t, err)
	assert.Equal(t, int64(349_999), got.Amount) // the observer flags the mismatch
}

func TestQueryRejectsAForeignSession(t *testing.T) {
	p, api := newProvider(t)
	api.set(opRetrieveSession, reply{fixture: "session_complete_paid", override: map[string]any{"client_reference_id": "other"}})
	_, err := p.Query(bg, stripePayment(testSessionID))
	require.Error(t, err)
}

func TestQueryWithoutSession(t *testing.T) {
	t.Run("no provider ref", func(t *testing.T) {
		p, api := newProvider(t)
		pay := stripePayment("")
		got, err := p.Query(bg, pay)
		require.NoError(t, err)
		assert.Equal(t, domain.ObservedPending, got.State)
		pay.ExpiresAt = testNow.Add(-time.Minute)
		got, err = p.Query(bg, pay)
		require.NoError(t, err)
		assert.Equal(t, domain.ObservedExpired, got.State)
		assert.Empty(t, api.calls(""))
	})
	t.Run("session missing at Stripe", func(t *testing.T) {
		p, api := newProvider(t)
		api.set(opRetrieveSession, reply{fixture: "error_resource_missing", status: http.StatusNotFound})
		pay := stripePayment(testSessionID)
		got, err := p.Query(bg, pay)
		require.NoError(t, err)
		assert.Equal(t, domain.ObservedPending, got.State)
		pay.ExpiresAt = testNow.Add(-time.Minute)
		got, err = p.Query(bg, pay)
		require.NoError(t, err)
		assert.Equal(t, domain.ObservedExpired, got.State)
	})
	t.Run("5xx", func(t *testing.T) {
		p, api := newProvider(t)
		api.set(opRetrieveSession, reply{fixture: "error_api", status: http.StatusInternalServerError})
		_, err := p.Query(bg, stripePayment(testSessionID))
		require.ErrorIs(t, err, app.ErrProviderUnavailable)
	})
}

func TestCancel(t *testing.T) {
	t.Run("open session is expired", func(t *testing.T) {
		p, api := newProvider(t)
		api.set(opExpireSession, reply{fixture: "session_expired"})
		ok, err := p.Cancel(bg, stripePayment(testSessionID))
		require.NoError(t, err)
		assert.True(t, ok)
		calls := api.calls(opExpireSession)
		require.Len(t, calls, 1)
		assert.Equal(t, "/v1/checkout/sessions/"+testSessionID+"/expire", calls[0].path)
	})
	t.Run("complete session cannot be cancelled", func(t *testing.T) {
		p, api := newProvider(t)
		api.set(opExpireSession, reply{fixture: "error_session_not_open", status: http.StatusBadRequest})
		api.set(opRetrieveSession, reply{fixture: "session_complete_paid"})
		ok, err := p.Cancel(bg, stripePayment(testSessionID))
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("already expired session counts as cancelled", func(t *testing.T) {
		p, api := newProvider(t)
		api.set(opExpireSession, reply{fixture: "error_session_not_open", status: http.StatusBadRequest})
		api.set(opRetrieveSession, reply{fixture: "session_expired"})
		ok, err := p.Cancel(bg, stripePayment(testSessionID))
		require.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("no session", func(t *testing.T) {
		p, api := newProvider(t)
		ok, err := p.Cancel(bg, stripePayment(""))
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Empty(t, api.calls(""))
	})
	t.Run("5xx", func(t *testing.T) {
		p, api := newProvider(t)
		api.set(opExpireSession, reply{fixture: "error_api", status: http.StatusServiceUnavailable})
		_, err := p.Cancel(bg, stripePayment(testSessionID))
		require.ErrorIs(t, err, app.ErrProviderUnavailable)
	})
}

func TestRefund(t *testing.T) {
	pay := stripePayment(testSessionID)
	pay.Status, pay.ProviderTxnID = domain.StatusSucceeded, testIntentID
	for _, tc := range []struct {
		fixture, status string
		want            app.RefundState
	}{
		{"refund_pending", "", app.RefundPending},
		{"refund_pending", "requires_action", app.RefundPending},
		{"refund_succeeded", "", app.RefundSucceeded},
		{"refund_failed", "", app.RefundFailed},
		{"refund_failed", "canceled", app.RefundFailed},
	} {
		t.Run(tc.fixture+"/"+tc.status, func(t *testing.T) {
			p, api := newProvider(t)
			var ov map[string]any
			if tc.status != "" {
				ov = map[string]any{"status": tc.status}
			}
			api.set(opCreateRefund, reply{fixture: tc.fixture, override: ov})
			got, err := p.Refund(bg, app.RefundInput{RefundID: "0192f0c1-ffff-7000-8000-00000000000a", Payment: pay, Amount: 120_000})
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.State)
			assert.Equal(t, testRefundRef, got.ProviderRefundRef)
			if tc.want == app.RefundFailed {
				assert.NotEmpty(t, got.Reason)
			}
			calls := api.calls(opCreateRefund)
			require.Len(t, calls, 1)
			assert.Equal(t, "0192f0c1-ffff-7000-8000-00000000000a", calls[0].header.Get("Idempotency-Key"))
			assert.Equal(t, testIntentID, calls[0].form.Get("payment_intent"))
			assert.Equal(t, "120000", calls[0].form.Get("amount"))
			assert.Equal(t, "0192f0c1-ffff-7000-8000-00000000000a", calls[0].form.Get("metadata[refund_id]"))
		})
	}
	t.Run("5xx", func(t *testing.T) {
		p, api := newProvider(t)
		api.set(opCreateRefund, reply{fixture: "error_api", status: http.StatusInternalServerError})
		_, err := p.Refund(bg, app.RefundInput{RefundID: "r1", Payment: pay, Amount: 1})
		require.ErrorIs(t, err, app.ErrProviderUnavailable)
	})
	t.Run("no payment intent", func(t *testing.T) {
		p, api := newProvider(t)
		_, err := p.Refund(bg, app.RefundInput{RefundID: "r1", Payment: stripePayment(testSessionID), Amount: 1})
		require.Error(t, err)
		assert.Empty(t, api.calls(""))
	})
}

func TestQueryRefund(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		want    app.RefundState
	}{
		{"refund_pending", app.RefundPending},
		{"refund_succeeded", app.RefundSucceeded},
		{"refund_failed", app.RefundFailed},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			p, api := newProvider(t)
			api.set(opRetrieveRefund, reply{fixture: tc.fixture})
			got, err := p.QueryRefund(bg, app.Refund{ID: "r1", PaymentID: payID, ProviderRefundRef: testRefundRef})
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.State)
			assert.Equal(t, testRefundRef, got.ProviderRefundRef)
			calls := api.calls(opRetrieveRefund)
			require.Len(t, calls, 1)
			assert.Equal(t, "/v1/refunds/"+testRefundRef, calls[0].path)
		})
	}
	t.Run("never requested", func(t *testing.T) {
		p, api := newProvider(t)
		got, err := p.QueryRefund(bg, app.Refund{ID: "r1", PaymentID: payID})
		require.NoError(t, err)
		assert.Equal(t, app.RefundPending, got.State)
		assert.Empty(t, api.calls(""))
	})
}
