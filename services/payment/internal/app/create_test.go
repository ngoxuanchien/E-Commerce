package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	orderv1 "github.com/ngoxuanchien/e-commerce/gen/order/v1"

	"github.com/ngoxuanchien/e-commerce/pkg/idemkey"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/fake"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/order"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/order/orderfake"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// countingProvider is a Stripe stand-in that counts session creations and can fail them.
type countingProvider struct {
	stubProvider
	sessions int
	failWith error
}

func (c *countingProvider) CreateSession(ctx context.Context, in app.SessionInput) (app.Session, error) {
	c.mu.Lock()
	c.sessions++
	err := c.failWith
	c.mu.Unlock()
	if err != nil {
		return app.Session{}, err
	}
	return c.stubProvider.CreateSession(ctx, in)
}

func (c *countingProvider) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions
}

var _ app.Provider = (*countingProvider)(nil)

type createEnv struct {
	env
	creator *app.Creator
	orders  *orderfake.Fake
	fake    *fake.Provider
	stripe  *countingProvider
	buyer   string
	now     time.Time
	mu      sync.Mutex
}

func newCreateEnv(t *testing.T) *createEnv {
	t.Helper()
	e := &createEnv{env: newEnv(t), orders: orderfake.New(), buyer: id(), now: t0}
	clock := func() time.Time {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.now
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.fake = fake.New(e.pool, fake.Config{PublicBaseURL: "http://gw.test", WebhookURL: "http://127.0.0.1:1/never", WebhookSecret: "s", Now: clock, Logger: log})
	t.Cleanup(e.fake.Wait)
	e.stripe = &countingProvider{stubProvider: stubProvider{name: "stripe"}}
	client := order.New(e.orders.Dial(t), order.Config{Timeout: time.Second, Retries: 0, Backoff: time.Millisecond})
	obs := app.NewObserver(e.store, clock, log)
	e.creator = app.NewCreator(e.store, e.pool, client, app.NewProviders(e.fake, e.stripe),
		obs, app.CreateConfig{PublicBaseURL: "http://gw.test", StripeMinAmount: 12_000}, clock)
	e.obs = obs
	return e
}

func (e *createEnv) checkout(method string, totals ...int64) *appCheckout {
	co := orderfake.NewCheckout(e.buyer, method, t0.Add(15*time.Minute), totals...)
	e.orders.Put(co)
	return &appCheckout{id: co.GetCheckoutId()}
}

type appCheckout struct{ id string }

// enc stores {"status": ..., "reason"|"id": ...} so tests can read what was stored.
func enc(p domain.Payment, err error) idemkey.Response {
	var st *app.StateError
	var body map[string]any
	status := 201
	switch {
	case err == nil:
		body = map[string]any{"id": p.ID}
	case errors.As(err, &st):
		status, body = 409, map[string]any{"reason": st.Reason}
	default:
		status, body = 404, map[string]any{"error": "not found"}
	}
	b, _ := json.Marshal(body)
	return idemkey.Response{Status: status, Body: b}
}

func (e *createEnv) create(key, checkoutID string) (idemkey.Response, error) {
	return e.creator.Create(ctx, app.CreateInput{BuyerID: e.buyer, IdempotencyKey: key, CheckoutID: checkoutID, ClientIP: "127.0.0.1"}, enc)
}

func (e *createEnv) mustCreate(t *testing.T, key, checkoutID string) domain.Payment {
	t.Helper()
	resp, err := e.create(key, checkoutID)
	require.NoError(t, err)
	require.Equal(t, 201, resp.Status, string(resp.Body))
	var b struct{ ID string }
	require.NoError(t, json.Unmarshal(resp.Body, &b))
	p, err := e.store.GetPayment(ctx, b.ID)
	require.NoError(t, err)
	return p
}

// sameResponse compares status and JSON content: the store is jsonb, so byte formatting may differ.
func sameResponse(t *testing.T, want, got idemkey.Response) {
	t.Helper()
	assert.Equal(t, want.Status, got.Status)
	assert.JSONEq(t, string(want.Body), string(got.Body))
}

func reasonOf(t *testing.T, r idemkey.Response) string {
	t.Helper()
	require.Equal(t, 409, r.Status, string(r.Body))
	var b struct{ Reason string }
	require.NoError(t, json.Unmarshal(r.Body, &b))
	return b.Reason
}

func TestCreateHappyPath(t *testing.T) {
	e := newCreateEnv(t)
	co := e.checkout("VNPAY", 100_000, 250_000)

	p := e.mustCreate(t, "k1", co.id)
	assert.Equal(t, domain.StatusPending, p.Status)
	assert.EqualValues(t, 350_000, p.Amount)
	assert.Equal(t, "VNPAY", p.Method)
	assert.Equal(t, "fake", p.Provider)
	assert.Equal(t, co.id, p.CheckoutID)
	assert.Equal(t, e.buyer, p.BuyerID)
	assert.True(t, p.ExpiresAt.Equal(t0.Add(15*time.Minute)), "expires_at is the payment deadline")
	assert.Equal(t, "http://gw.test/api/payments/fake/checkout/"+p.ID, p.RedirectURL)
	assert.Equal(t, "fake_"+p.ID, p.ProviderRef)

	var allocs []app.Allocation
	require.NoError(t, e.store.InTx(ctx, func(tx app.Tx) error {
		var err error
		allocs, err = tx.Allocations(ctx, p.ID)
		return err
	}))
	assert.Len(t, allocs, 2)
	var sum int64
	for _, a := range allocs {
		sum += a.Amount
	}
	assert.EqualValues(t, 350_000, sum)
}

func TestCreatePreconditions(t *testing.T) {
	e := newCreateEnv(t)
	deadlinePast := orderfake.NewCheckout(e.buyer, "VNPAY", t0.Add(-time.Second), 100_000)
	noDeadline := orderfake.NewCheckout(e.buyer, "VNPAY", time.Time{}, 100_000)
	atDeadline := orderfake.NewCheckout(e.buyer, "VNPAY", t0, 100_000)
	cod := orderfake.NewCheckout(e.buyer, "COD", t0.Add(time.Hour), 100_000)
	codLate := orderfake.NewCheckout(e.buyer, "COD", time.Time{}, 100_000) // COD wins over the deadline rule
	notAwaiting := orderfake.NewCheckout(e.buyer, "VNPAY", t0.Add(time.Hour), 100_000)
	stranger := orderfake.NewCheckout(id(), "VNPAY", t0.Add(time.Hour), 100_000)
	for _, co := range []*orderv1.GetCheckoutResponse{deadlinePast, noDeadline, atDeadline, cod, codLate, notAwaiting, stranger} {
		e.orders.Put(co)
	}
	e.orders.SetOrderStatus(notAwaiting.GetCheckoutId(), "PAID")

	for _, c := range []struct {
		name, checkout, reason string
		status                 int
	}{
		{"unknown checkout", id(), "", 404},
		{"another buyer's checkout", stranger.GetCheckoutId(), "", 404},
		{"COD", cod.GetCheckoutId(), "COD_CHECKOUT", 409},
		{"COD without a deadline", codLate.GetCheckoutId(), "COD_CHECKOUT", 409},
		{"no order awaiting payment", notAwaiting.GetCheckoutId(), "NOT_AWAITING_PAYMENT", 409},
		{"deadline passed", deadlinePast.GetCheckoutId(), "DEADLINE_PASSED", 409},
		{"deadline is now", atDeadline.GetCheckoutId(), "DEADLINE_PASSED", 409},
		{"no deadline", noDeadline.GetCheckoutId(), "DEADLINE_PASSED", 409},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp, err := e.create("k-"+c.name, c.checkout)
			require.NoError(t, err)
			assert.Equal(t, c.status, resp.Status, string(resp.Body))
			if c.reason != "" {
				assert.Equal(t, c.reason, reasonOf(t, resp))
			}
			// The rejection is stored: a replay returns it verbatim without calling order-svc again.
			calls := e.orders.Calls()
			again, err := e.create("k-"+c.name, c.checkout)
			require.NoError(t, err)
			sameResponse(t, resp, again)
			assert.Equal(t, calls, e.orders.Calls())
		})
	}
}

func TestCreateOnlyAwaitingOrdersAreCharged(t *testing.T) {
	e := newCreateEnv(t)
	co := orderfake.NewCheckout(e.buyer, "VNPAY", t0.Add(time.Hour), 100_000, 200_000)
	co.Orders[1].Status = "CANCELLED"
	e.orders.Put(co)
	p := e.mustCreate(t, "k", co.GetCheckoutId())
	assert.EqualValues(t, 100_000, p.Amount)
}

func TestCreateAlreadyPaid(t *testing.T) {
	e := newCreateEnv(t)
	co := e.checkout("VNPAY", 100_000)
	p := e.mustCreate(t, "k1", co.id)
	require.NoError(t, e.fake.Submit(ctx, p.ID, "succeed", fake.Hooks{Webhook: "drop"}))
	obs, err := e.fake.Query(ctx, p)
	require.NoError(t, err)
	_, err = e.obs.ApplyObserved(ctx, "fake", "", p.ID, obs)
	require.NoError(t, err)

	resp, err := e.create("k2", co.id)
	require.NoError(t, err)
	assert.Equal(t, "ALREADY_PAID", reasonOf(t, resp))
}

func TestCreateReplayAndKeyReuse(t *testing.T) {
	e := newCreateEnv(t)
	a, b := e.checkout("VNPAY", 100_000), e.checkout("VNPAY", 200_000)

	first, err := e.create("k", a.id)
	require.NoError(t, err)
	again, err := e.create("k", a.id)
	require.NoError(t, err)
	sameResponse(t, first, again)
	ps, err := e.store.ListCheckoutPayments(ctx, a.id)
	require.NoError(t, err)
	assert.Len(t, ps, 1, "the replay created no second attempt")

	_, err = e.create("k", b.id)
	assert.ErrorIs(t, err, app.ErrKeyReused)
}

func TestCreateInProgress(t *testing.T) {
	e := newCreateEnv(t)
	co := e.checkout("VNPAY", 100_000)
	// The first request is held up by a slow order-svc while its twin arrives.
	e.orders.SetDelay(300 * time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := e.create("slow", co.id); done <- err }()
	require.Eventually(t, func() bool { return e.orders.Calls() >= 1 }, 2*time.Second, 5*time.Millisecond)

	_, err := e.create("slow", co.id)
	assert.ErrorIs(t, err, app.ErrKeyInProgress)
	require.NoError(t, <-done)
}

func TestCreateReplacesPreviousPendingAttempt(t *testing.T) {
	e := newCreateEnv(t)
	co := e.checkout("VNPAY", 100_000)
	first := e.mustCreate(t, "k1", co.id)
	second := e.mustCreate(t, "k2", co.id)
	assert.NotEqual(t, first.ID, second.ID)

	old, err := e.store.GetPayment(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusExpired, old.Status)
	assert.Equal(t, domain.StatusPending, second.Status)
	ps, err := e.store.ListCheckoutPayments(ctx, co.id)
	require.NoError(t, err)
	pending := 0
	for _, p := range ps {
		if p.Status == domain.StatusPending {
			pending++
		}
	}
	assert.Equal(t, 1, pending)
}

func TestCreateExpiresAnAttemptTheProviderCannotCancel(t *testing.T) {
	e := newCreateEnv(t)
	co := e.checkout("VNPAY", 100_000)
	first := e.mustCreate(t, "k1", co.id)
	require.NoError(t, e.fake.ArmHooks(ctx, first.ID, fake.Hooks{Cancel: "unsupported"}))

	second := e.mustCreate(t, "k2", co.id)
	old, err := e.store.GetPayment(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusExpired, old.Status, "payment-svc expired it itself")
	assert.Equal(t, domain.StatusPending, second.Status)
}

func TestCreateRetiringAPaidAttemptAnswersAlreadyPaid(t *testing.T) {
	e := newCreateEnv(t)
	co := e.checkout("VNPAY", 100_000)
	first := e.mustCreate(t, "k1", co.id)
	// Paid at the provider, but no webhook has been applied yet.
	require.NoError(t, e.fake.Submit(ctx, first.ID, "succeed", fake.Hooks{Webhook: "drop"}))

	resp, err := e.create("k2", co.id)
	require.NoError(t, err)
	assert.Equal(t, "ALREADY_PAID", reasonOf(t, resp))
	cur, err := e.store.GetPayment(ctx, first.ID)
	require.NoError(t, err)
	assert.True(t, cur.Captured, "the observed success was applied")
	assert.Equal(t, domain.StatusSucceeded, cur.Status)
}

func TestCreateStripeMinimum(t *testing.T) {
	e := newCreateEnv(t)
	below := e.checkout("STRIPE", 11_999)
	resp, err := e.create("k1", below.id)
	require.NoError(t, err)
	assert.Equal(t, "BELOW_PROVIDER_MINIMUM", reasonOf(t, resp))
	assert.Equal(t, 0, e.stripe.count(), "no provider call below the minimum")

	exact := e.checkout("STRIPE", 12_000)
	p := e.mustCreate(t, "k2", exact.id)
	assert.Equal(t, "stripe", p.Provider)
	assert.Equal(t, 1, e.stripe.count())
}

func TestCreateProviderErrorFailsThePaymentAndReleasesTheKey(t *testing.T) {
	e := newCreateEnv(t)
	co := e.checkout("STRIPE", 100_000)
	e.stripe.failWith = errors.New("boom")

	_, err := e.create("k", co.id)
	require.ErrorIs(t, err, app.ErrProviderUnavailable)
	ps, err := e.store.ListCheckoutPayments(ctx, co.id)
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Equal(t, domain.StatusFailed, ps[0].Status)
	assert.Equal(t, "PROVIDER_ERROR", ps[0].FailureReason)

	e.stripe.failWith = nil
	p := e.mustCreate(t, "k", co.id) // same key: released, so this is a fresh attempt
	assert.Equal(t, domain.StatusPending, p.Status)
	assert.NotEqual(t, ps[0].ID, p.ID)
}

func TestCreateOrdersUnavailableReleasesTheKey(t *testing.T) {
	e := newCreateEnv(t)
	co := e.checkout("VNPAY", 100_000)
	e.orders.FailNext(codes.Unavailable, 1)

	_, err := e.create("k", co.id)
	require.ErrorIs(t, err, app.ErrOrdersUnavailable)
	ps, err := e.store.ListCheckoutPayments(ctx, co.id)
	require.NoError(t, err)
	assert.Empty(t, ps)

	p := e.mustCreate(t, "k", co.id)
	assert.Equal(t, domain.StatusPending, p.Status)
}

func TestGetIsScopedToTheOwner(t *testing.T) {
	e := newCreateEnv(t)
	co := e.checkout("VNPAY", 100_000)
	p := e.mustCreate(t, "k", co.id)

	got, err := e.creator.Get(ctx, e.buyer, p.ID)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)
	_, err = e.creator.Get(ctx, id(), p.ID)
	assert.ErrorIs(t, err, app.ErrNotFound)
	_, err = e.creator.Get(ctx, e.buyer, id())
	assert.ErrorIs(t, err, app.ErrNotFound)
}
