package stripe_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/stripe"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/payment/migrations"
)

func uid() string { return uuid.Must(uuid.NewV7()).String() }

type cbEnv struct {
	t     *testing.T
	pool  *pgxpool.Pool
	store *postgres.Store
	api   *apiStub
	cb    stripe.Callbacks
}

// newCBEnv wires Callbacks against a real Postgres and the Stripe stub. The webhook tolerance is
// checked against the wall clock, so the adapter runs on time.Now here.
func newCBEnv(t *testing.T) *cbEnv {
	t.Helper()
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	pool, err := pgxx.NewPool(bg, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store := postgres.NewStore(pool)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := newAPIStub(t)
	cfg := testConfig(api.srv.URL, time.Now())
	cfg.Now = time.Now
	prov := stripe.New(cfg)
	return &cbEnv{t: t, pool: pool, store: store, api: api,
		cb: stripe.NewCallbacks(prov, store, app.NewObserver(store, time.Now, log), log)}
}

// payment inserts a PENDING Stripe payment with one allocation and its Checkout Session.
func (e *cbEnv) payment(amount int64) (domain.Payment, string) {
	e.t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := domain.Payment{
		ID: uid(), CheckoutID: uid(), BuyerID: uid(), Method: "STRIPE", Provider: "stripe",
		Status: domain.StatusPending, Amount: amount, ExpiresAt: now.Add(15 * time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	session := "cs_test_" + uuid.NewString()[:8]
	np := app.NewPayment{Payment: p, Allocations: []app.Allocation{{OrderID: uid(), SellerUserID: uid(), Amount: amount}}}
	require.NoError(e.t, e.store.InTx(bg, func(tx app.Tx) error {
		if err := tx.InsertPayment(bg, np); err != nil {
			return err
		}
		return tx.SetSession(bg, p.ID, session, "https://checkout.stripe.com/c/pay/"+session)
	}))
	return e.get(p.ID), session
}

// serveSession makes the stub's session Retrieve answer fixture for p.
func (e *cbEnv) serveSession(fixture string, p domain.Payment, session string) {
	e.api.set(opRetrieveSession, reply{fixture: fixture, override: sessionFor(session, p.ID, p.CheckoutID, p.Amount)})
}

func (e *cbEnv) get(id string) domain.Payment {
	e.t.Helper()
	p, err := e.store.GetPayment(bg, id)
	require.NoError(e.t, err)
	return p
}

func (e *cbEnv) outbox(p domain.Payment) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.pool.QueryRow(bg, `SELECT count(*) FROM outbox WHERE msg_key = $1`, p.CheckoutID).Scan(&n))
	return n
}

// outboxTypes lists the event_type of every outbox row for p's checkout, oldest first.
func (e *cbEnv) outboxTypes(p domain.Payment) []string {
	e.t.Helper()
	var types []string
	require.NoError(e.t, e.pool.QueryRow(bg,
		`SELECT coalesce(array_agg(payload->>'event_type' ORDER BY created_at, id), '{}') FROM outbox WHERE msg_key = $1`,
		p.CheckoutID).Scan(&types))
	return types
}

func (e *cbEnv) deliver(body []byte) int {
	return e.cb.Webhook(bg, body, signature(testWebhookSecret, body, time.Now()))
}

func TestWebhookAppliesTheQueriedSession(t *testing.T) {
	e := newCBEnv(t)
	p, session := e.payment(350_000)
	e.serveSession("session_complete_paid", p, session)

	body := event(t, "event_checkout_session_completed", "evt_"+uid(), session, p.ID)
	assert.Equal(t, http.StatusOK, e.deliver(body))

	got := e.get(p.ID)
	assert.Equal(t, domain.StatusSucceeded, got.Status)
	assert.True(t, got.Captured)
	assert.Equal(t, testIntentID, got.ProviderTxnID)
	assert.Equal(t, 1, e.outbox(p))
	calls := e.api.calls(opRetrieveSession)
	require.Len(t, calls, 1)
	assert.Equal(t, "/v1/checkout/sessions/"+session, calls[0].path)
}

// Review Focus 4: the signature is checked over the raw bytes before anything is parsed or queried.
func TestWebhookRejectsBadSignatures(t *testing.T) {
	e := newCBEnv(t)
	p, session := e.payment(350_000)
	e.serveSession("session_complete_paid", p, session)
	body := event(t, "event_checkout_session_completed", "evt_"+uid(), session, p.ID)

	for name, sig := range map[string]string{
		"wrong secret":  signature("whsec_wrong", body, time.Now()),
		"stale":         signature(testWebhookSecret, body, time.Now().Add(-10*time.Minute)),
		"missing":       "",
		"garbage":       "t=abc,v1=zz",
		"tampered body": signature(testWebhookSecret, append([]byte(" "), body...), time.Now()),
	} {
		assert.Equal(t, http.StatusBadRequest, e.cb.Webhook(bg, body, sig), name)
	}
	assert.Equal(t, http.StatusBadRequest, e.cb.Webhook(bg, []byte("not json"), signature(testWebhookSecret, []byte("not json"), time.Now())))

	assert.Equal(t, domain.StatusPending, e.get(p.ID).Status)
	assert.Equal(t, 0, e.outbox(p))
	assert.Empty(t, e.api.calls(""))
}

func TestWebhookWithoutASecretRejectsEverything(t *testing.T) {
	api := newAPIStub(t)
	cfg := testConfig(api.srv.URL, time.Now())
	cfg.WebhookSecret = ""
	cb := stripe.NewCallbacks(stripe.New(cfg), nil, nil, nil)
	body := event(t, "event_checkout_session_completed", "evt_1", testSessionID, payID)
	assert.Equal(t, http.StatusBadRequest, cb.Webhook(bg, body, signature("", body, time.Now())))
	assert.Empty(t, api.calls(""))
}

// Review Focus 1: the event is only a hint; an expired event delivered after completed is resolved by
// re-querying the (complete) session, so the payment stays SUCCEEDED.
func TestWebhookExpiredAfterCompletedStaysSucceeded(t *testing.T) {
	e := newCBEnv(t)
	p, session := e.payment(350_000)
	e.serveSession("session_complete_paid", p, session)

	require.Equal(t, http.StatusOK, e.deliver(event(t, "event_checkout_session_completed", "evt_"+uid(), session, p.ID)))
	require.Equal(t, http.StatusOK, e.deliver(event(t, "event_checkout_session_expired", "evt_"+uid(), session, p.ID)))

	assert.Equal(t, domain.StatusSucceeded, e.get(p.ID).Status)
	assert.Equal(t, 1, e.outbox(p))
	assert.Len(t, e.api.calls(opRetrieveSession), 2)
}

func TestWebhookExpiredDeliveredFirstStillApplies(t *testing.T) {
	e := newCBEnv(t)
	p, session := e.payment(350_000)
	e.serveSession("session_complete_paid", p, session)
	require.Equal(t, http.StatusOK, e.deliver(event(t, "event_checkout_session_expired", "evt_"+uid(), session, p.ID)))
	assert.Equal(t, domain.StatusSucceeded, e.get(p.ID).Status)

	q, qs := e.payment(350_000)
	e.serveSession("session_expired", q, qs)
	require.Equal(t, http.StatusOK, e.deliver(event(t, "event_checkout_session_expired", "evt_"+uid(), qs, q.ID)))
	assert.Equal(t, domain.StatusExpired, e.get(q.ID).Status)
}

// A redelivered event.id is deduplicated: it is not applied a second time even when Stripe's state moved.
func TestWebhookDuplicateEventID(t *testing.T) {
	e := newCBEnv(t)
	p, session := e.payment(350_000)
	id := "evt_" + uid()
	body := event(t, "event_checkout_session_completed", id, session, p.ID)

	e.serveSession("session_open", p, session)
	require.Equal(t, http.StatusOK, e.deliver(body))
	assert.Equal(t, domain.StatusPending, e.get(p.ID).Status)

	e.serveSession("session_complete_paid", p, session)
	assert.Equal(t, http.StatusOK, e.deliver(body))
	assert.Equal(t, domain.StatusPending, e.get(p.ID).Status)
	assert.Equal(t, 0, e.outbox(p))

	// A new event id is applied.
	require.Equal(t, http.StatusOK, e.deliver(event(t, "event_checkout_session_completed", "evt_"+uid(), session, p.ID)))
	assert.Equal(t, domain.StatusSucceeded, e.get(p.ID).Status)
	assert.Equal(t, 1, e.outbox(p))
}

func TestWebhookIgnoresWhatItCannotApply(t *testing.T) {
	e := newCBEnv(t)
	p, session := e.payment(350_000)
	e.serveSession("session_complete_paid", p, session)

	for name, body := range map[string][]byte{
		"unknown payment": event(t, "event_checkout_session_completed", "evt_"+uid(), session, uid()),
		"invalid id":      event(t, "event_checkout_session_completed", "evt_"+uid(), session, "not-a-uuid"),
		"no metadata":     event(t, "event_checkout_session_completed", "evt_"+uid(), session, ""),
		"other session":   event(t, "event_checkout_session_completed", "evt_"+uid(), "cs_test_other", p.ID),
		"ignored type":    event(t, "event_customer_created", "evt_"+uid(), "", ""),
		"refund, no hook": event(t, "event_refund_updated", "evt_"+uid(), "", ""),
		"charge, no hook": event(t, "event_charge_refunded", "evt_"+uid(), "", ""),
	} {
		assert.Equal(t, http.StatusOK, e.deliver(body), name)
	}
	assert.Equal(t, domain.StatusPending, e.get(p.ID).Status)
	assert.Empty(t, e.api.calls(""))
}

func TestWebhookInternalErrorsAsk500(t *testing.T) {
	e := newCBEnv(t)
	p, session := e.payment(350_000)
	e.api.set(opRetrieveSession, reply{fixture: "error_api", status: http.StatusInternalServerError})
	body := event(t, "event_checkout_session_completed", "evt_"+uid(), session, p.ID)
	assert.Equal(t, http.StatusInternalServerError, e.deliver(body))
	assert.Equal(t, domain.StatusPending, e.get(p.ID).Status)

	// Stripe retries the same event; once the provider answers it is applied.
	e.serveSession("session_complete_paid", p, session)
	assert.Equal(t, http.StatusOK, e.deliver(body))
	assert.Equal(t, domain.StatusSucceeded, e.get(p.ID).Status)
}

// async_payment_failed resolves to FAILED (with one PaymentFailed) through the queried session:
// complete + unpaid with the PaymentIntent back in requires_payment_method.
func TestWebhookAsyncPaymentFailed(t *testing.T) {
	e := newCBEnv(t)
	p, session := e.payment(350_000)
	e.serveSession("session_async_failed", p, session)
	body := event(t, "event_checkout_session_async_payment_failed", "evt_"+uid(), session, p.ID)

	require.Equal(t, http.StatusOK, e.deliver(body))
	got := e.get(p.ID)
	assert.Equal(t, domain.StatusFailed, got.Status)
	assert.Equal(t, "stripe_async_failed", got.FailureReason)
	assert.Equal(t, []string{"PaymentFailed"}, e.outboxTypes(p))

	// A redelivery, or a later event for the same failure, emits nothing more.
	require.Equal(t, http.StatusOK, e.deliver(body))
	require.Equal(t, http.StatusOK, e.deliver(event(t, "event_checkout_session_async_payment_failed", "evt_"+uid(), session, p.ID)))
	assert.Equal(t, []string{"PaymentFailed"}, e.outboxTypes(p))
}

func TestWebhookAsyncPaymentSucceeded(t *testing.T) {
	e := newCBEnv(t)
	p, session := e.payment(350_000)
	e.serveSession("session_complete_paid", p, session)
	require.Equal(t, http.StatusOK, e.deliver(event(t, "event_checkout_session_async_payment_succeeded", "evt_"+uid(), session, p.ID)))
	assert.Equal(t, domain.StatusSucceeded, e.get(p.ID).Status)
	assert.Equal(t, []string{"PaymentSucceeded"}, e.outboxTypes(p))
}

func TestWebhookRefundHint(t *testing.T) {
	e := newCBEnv(t)
	var got []stripe.RefundHintInput
	e.cb.RefundHint = func(_ context.Context, in stripe.RefundHintInput) error {
		got = append(got, in)
		return nil
	}
	assert.Equal(t, http.StatusOK, e.deliver(event(t, "event_refund_updated", "evt_"+uid(), "", "")))
	assert.Equal(t, http.StatusOK, e.deliver(event(t, "event_charge_refunded", "evt_"+uid(), "", "")))
	want := stripe.RefundHintInput{ProviderRefundRef: testRefundRef, RefundID: testRefundID}
	assert.Equal(t, []stripe.RefundHintInput{want, want}, got)

	// A dashboard refund carries no metadata.refund_id: the hint still gets the Stripe ref.
	got = nil
	ev := loadFixture(t, "event_refund_updated")
	ev["id"] = "evt_" + uid()
	delete(ev["data"].(map[string]any)["object"].(map[string]any), "metadata")
	raw, err := json.Marshal(ev)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, e.deliver(raw))
	assert.Equal(t, []stripe.RefundHintInput{{ProviderRefundRef: testRefundRef}}, got)

	e.cb.RefundHint = func(context.Context, stripe.RefundHintInput) error { return errors.New("db down") }
	assert.Equal(t, http.StatusInternalServerError, e.deliver(event(t, "event_refund_updated", "evt_"+uid(), "", "")))
}

func TestReturnAppliesTheQueriedSession(t *testing.T) {
	e := newCBEnv(t)
	p, session := e.payment(350_000)
	e.serveSession("session_complete_paid", p, session)

	assert.Equal(t, p.ID, e.cb.Return(bg, session))
	assert.Equal(t, domain.StatusSucceeded, e.get(p.ID).Status)
	assert.Equal(t, 1, e.outbox(p))

	// Already final: no further query.
	assert.Equal(t, p.ID, e.cb.Return(bg, session))
	assert.Len(t, e.api.calls(opRetrieveSession), 1)
}

func TestReturnUnknownOrFailing(t *testing.T) {
	e := newCBEnv(t)
	assert.Equal(t, "", e.cb.Return(bg, "cs_test_unknown"))
	assert.Equal(t, "", e.cb.Return(bg, ""))

	p, session := e.payment(350_000)
	e.api.set(opRetrieveSession, reply{fixture: "error_api", status: http.StatusInternalServerError})
	assert.Equal(t, p.ID, e.cb.Return(bg, session)) // logged; the webhook and the reconciler catch up
	assert.Equal(t, domain.StatusPending, e.get(p.ID).Status)
}
