package app_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/testsupport"
)

const minPayout = 50000

// fund releases amount (commission 0) into seller's wallet.
func (h *harness) fund(seller uuid.UUID, amount int64) {
	h.t.Helper()
	c := newCheckout("fake", orderSpec{amount: amount, seller: seller})
	h.deliver(c.succeeded())
	h.deliver(c.completed(0))
}

func (h *harness) payouts() *app.PayoutService {
	return app.NewPayoutService(h.store, app.PayoutConfig{MinAmount: minPayout, Now: h.clock.Now, Logger: h.logger})
}

// outbox returns the (key, payload) of every message written to topic, oldest first.
func (h *harness) outbox(topic string) (keys []string, payloads [][]byte) {
	h.t.Helper()
	rows, err := h.db.Query(ctx, `SELECT msg_key, payload FROM outbox WHERE topic = $1 ORDER BY created_at, id`, topic)
	require.NoError(h.t, err)
	defer rows.Close()
	for rows.Next() {
		var k string
		var p []byte
		require.NoError(h.t, rows.Scan(&k, &p))
		keys, payloads = append(keys, k), append(payloads, p)
	}
	require.NoError(h.t, rows.Err())
	return keys, payloads
}

// testEncoder renders results the way the HTTP adapter's encoder does, minimally.
func testEncoder(p domain.Payout, err error) app.Response {
	var ife *app.InsufficientFundsError
	if errors.As(err, &ife) {
		b, _ := json.Marshal(map[string]any{"code": "INSUFFICIENT_FUNDS", "available": ife.Available})
		return app.Response{Status: http.StatusConflict, Body: b}
	}
	b, _ := json.Marshal(map[string]any{"id": p.ID.String(), "amount": p.Amount})
	return app.Response{Status: http.StatusCreated, Body: b}
}

// assertSameResponse compares status and JSON body semantically: the stored
// response is jsonb, so a replay may differ from the original in whitespace and key order.
func assertSameResponse(t *testing.T, want, got app.Response) {
	t.Helper()
	assert.Equal(t, want.Status, got.Status)
	assert.JSONEq(t, string(want.Body), string(got.Body))
}

func (h *harness) request(seller uuid.UUID, key string, amount int64) (app.Response, error) {
	return h.payouts().Request(ctx, app.PayoutRequestInput{SellerID: seller, IdempotencyKey: key, Amount: amount}, testEncoder)
}

func TestPayoutRequestMovesFundsToPending(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	h.fund(seller, 220000)
	resp, err := h.request(seller, "k1", 100000)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.Status)

	assert.Equal(t, int64(-120000), h.balance(wallet(seller)))
	assert.Equal(t, int64(-100000), h.balance("seller_payout_pending:"+seller.String()))
	var status string
	require.NoError(t, h.db.QueryRow(ctx, `SELECT status FROM payouts WHERE seller_user_id = $1`, seller).Scan(&status))
	assert.Equal(t, "REQUESTED", status)

	keys, payloads := h.outbox(event.TopicPayoutEvents)
	require.Len(t, payloads, 1)
	assert.Equal(t, seller.String(), keys[0])
	testsupport.AssertEventSchema(t, "payout-requested.schema.json", payloads[0])
}

func TestPayoutBelowMinimumIsRejectedWithoutClaimingTheKey(t *testing.T) {
	h := newHarness(t)
	_, err := h.request(newID(), "k1", minPayout-1)
	var bm *app.BelowMinimumError
	require.ErrorAs(t, err, &bm)
	assert.Equal(t, int64(minPayout), bm.Min)
	assert.Zero(t, h.count("idempotency_keys"))
}

func TestPayoutIdempotentReplay(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	h.fund(seller, 200000)
	first, err := h.request(seller, "k1", 60000)
	require.NoError(t, err)
	second, err := h.request(seller, "k1", 60000)
	require.NoError(t, err)
	assertSameResponse(t, first, second)
	assert.Equal(t, 1, h.count("payouts"))
	assert.Equal(t, int64(-140000), h.balance(wallet(seller)))
}

func TestPayoutKeyReusedWithDifferentAmount(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	h.fund(seller, 200000)
	_, err := h.request(seller, "k1", 60000)
	require.NoError(t, err)
	_, err = h.request(seller, "k1", 70000)
	assert.ErrorIs(t, err, app.ErrIdempotencyKeyReused)
}

func TestInsufficientFundsIsStoredAndReplayed(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	h.fund(seller, 100000)
	first, err := h.request(seller, "k1", 150000)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, first.Status)
	assert.JSONEq(t, `{"code":"INSUFFICIENT_FUNDS","available":100000}`, string(first.Body))

	h.fund(seller, 100000) // now 200000 available
	again, err := h.request(seller, "k1", 150000)
	require.NoError(t, err)
	assertSameResponse(t, first, again) // a completed key replays its stored 409
	fresh, err := h.request(seller, "k2", 150000)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, fresh.Status)
}

func TestSellerWithoutAccountsGetsInsufficientFunds(t *testing.T) {
	h := newHarness(t)
	resp, err := h.request(newID(), "k1", minPayout)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.Status)
	assert.JSONEq(t, `{"code":"INSUFFICIENT_FUNDS","available":0}`, string(resp.Body))
}

func TestConcurrentPayoutsNeverOverdraw(t *testing.T) {
	h := newHarness(t)
	seller := newID()
	h.fund(seller, 100000)
	svc := app.NewPayoutService(h.store, app.PayoutConfig{MinAmount: 1, Now: h.clock.Now, Logger: h.logger})
	var wg sync.WaitGroup
	statuses := make([]int, 10)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := svc.Request(ctx, app.PayoutRequestInput{SellerID: seller, IdempotencyKey: uuid.NewString(), Amount: 25000}, testEncoder)
			assert.NoError(t, err)
			statuses[i] = resp.Status
		}()
	}
	wg.Wait()
	created := 0
	for _, s := range statuses {
		if s == http.StatusCreated {
			created++
		}
	}
	assert.Equal(t, 4, created, "100000 / 25000")
	assert.Zero(t, h.balance(wallet(seller)))
	assert.Equal(t, int64(-100000), h.balance("seller_payout_pending:"+seller.String()))
}
