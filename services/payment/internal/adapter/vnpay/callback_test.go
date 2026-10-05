package vnpay_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/vnpay"
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
	cb    vnpay.Callbacks
}

func newCBEnv(t *testing.T) *cbEnv {
	t.Helper()
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	pool, err := pgxx.NewPool(bg, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store := postgres.NewStore(pool)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := newAPIStub(t, "querydr_paid")
	prov := newProvider(api.srv.URL, time.Now())
	return &cbEnv{t: t, pool: pool, store: store, api: api,
		cb: vnpay.NewCallbacks(prov, store, app.NewObserver(store, time.Now, log), log)}
}

// payment inserts a PENDING VNPay payment with one allocation and its session.
func (e *cbEnv) payment(amount int64) domain.Payment {
	e.t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := domain.Payment{
		ID: uid(), CheckoutID: uid(), BuyerID: uid(), Method: "VNPAY", Provider: "vnpay",
		Status: domain.StatusPending, Amount: amount, ExpiresAt: now.Add(15 * time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	np := app.NewPayment{Payment: p, Allocations: []app.Allocation{{OrderID: uid(), SellerUserID: uid(), Amount: amount}}}
	require.NoError(e.t, e.store.InTx(bg, func(tx app.Tx) error {
		if err := tx.InsertPayment(bg, np); err != nil {
			return err
		}
		return tx.SetSession(bg, p.ID, vnpay.TxnRef(p.ID), "https://sandbox.vnpayment.vn/x")
	}))
	got, err := e.store.GetPayment(bg, p.ID)
	require.NoError(e.t, err)
	return got
}

func (e *cbEnv) get(id string) domain.Payment {
	e.t.Helper()
	p, err := e.store.GetPayment(bg, id)
	require.NoError(e.t, err)
	return p
}

func (e *cbEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.pool.QueryRow(bg, sql, args...).Scan(&n))
	return n
}

func (e *cbEnv) outbox(p domain.Payment) int {
	return e.count(`SELECT count(*) FROM outbox WHERE msg_key = $1`, p.CheckoutID)
}

// ipnQuery is an IPN as VNPay sends it (shape of the docs' sample IPN URL), signed with the test secret.
func ipnQuery(p domain.Payment, amount int64, responseCode, txnStatus, txnNo string) url.Values {
	q := url.Values{
		"vnp_Amount": {itoa(amount * 100)}, "vnp_BankCode": {"NCB"}, "vnp_BankTranNo": {"VNP" + txnNo},
		"vnp_CardType": {"ATM"}, "vnp_OrderInfo": {"Thanh toan don hang " + p.CheckoutID},
		"vnp_PayDate": {"20260929153012"}, "vnp_ResponseCode": {responseCode}, "vnp_TmnCode": {testTmn},
		"vnp_TransactionNo": {txnNo}, "vnp_TransactionStatus": {txnStatus}, "vnp_TxnRef": {vnpay.TxnRef(p.ID)},
	}
	return sign(q)
}

func sign(q url.Values) url.Values {
	q.Del("vnp_SecureHash")
	q.Set("vnp_SecureHashType", "HmacSHA512")
	q.Set("vnp_SecureHash", vnpay.SecureHash(testSecret, q))
	return q
}

type ack struct{ code, msg string }

func (e *cbEnv) ipn(q url.Values) ack {
	c, m := e.cb.IPN(bg, q)
	return ack{c, m}
}

func TestIPNCodesInOrder(t *testing.T) {
	e := newCBEnv(t)

	t.Run("97 invalid checksum", func(t *testing.T) {
		p := e.payment(350_000)
		q := ipnQuery(p, 350_000, "00", "00", "1001")
		q.Set("vnp_Amount", "1") // tampered after signing; also a wrong amount, but the signature is checked first
		assert.Equal(t, ack{"97", "Invalid Checksum"}, e.ipn(q))
		assert.Equal(t, domain.StatusPending, e.get(p.ID).Status)
	})
	t.Run("01 other merchant", func(t *testing.T) {
		p := e.payment(350_000)
		q := ipnQuery(p, 1, "00", "00", "1002")
		q.Set("vnp_TmnCode", "OTHERTMN")
		assert.Equal(t, ack{"01", "Order not found"}, e.ipn(sign(q)))
	})
	t.Run("01 unknown TxnRef", func(t *testing.T) {
		p := e.payment(350_000)
		q := ipnQuery(p, 1, "00", "00", "1003")
		q.Set("vnp_TxnRef", vnpay.TxnRef(uid()))
		assert.Equal(t, ack{"01", "Order not found"}, e.ipn(sign(q)))
	})
	t.Run("04 invalid amount changes nothing", func(t *testing.T) {
		p := e.payment(350_000)
		events := e.count(`SELECT count(*) FROM provider_events WHERE provider = 'vnpay'`)
		assert.Equal(t, ack{"04", "Invalid amount"}, e.ipn(ipnQuery(p, 349_999, "00", "00", "1004")))
		got := e.get(p.ID)
		assert.Equal(t, domain.StatusPending, got.Status)
		assert.Equal(t, p.Version, got.Version)
		assert.Zero(t, e.outbox(p))
		assert.Equal(t, events, e.count(`SELECT count(*) FROM provider_events WHERE provider = 'vnpay'`),
			"a rejected IPN is not recorded, so VNPay's corrected retry is not taken for a duplicate")
	})
	t.Run("00 success then 02 on the duplicate", func(t *testing.T) {
		p := e.payment(350_000)
		q := ipnQuery(p, 350_000, "00", "00", "1005")
		assert.Equal(t, ack{"00", "Confirm Success"}, e.ipn(q))
		got := e.get(p.ID)
		assert.Equal(t, domain.StatusSucceeded, got.Status)
		assert.Equal(t, "1005", got.ProviderTxnID)
		assert.Equal(t, 1, e.outbox(p))

		assert.Equal(t, ack{"02", "Order already confirmed"}, e.ipn(q))
		assert.Equal(t, 1, e.outbox(p), "one event for a duplicate IPN")
		assert.Equal(t, got.Version, e.get(p.ID).Version)
	})
	t.Run("02 already final, different transaction", func(t *testing.T) {
		p := e.payment(350_000)
		require.Equal(t, "00", e.ipn(ipnQuery(p, 350_000, "00", "00", "1006")).code)
		events := e.count(`SELECT count(*) FROM provider_events WHERE provider = 'vnpay'`)
		assert.Equal(t, ack{"02", "Order already confirmed"}, e.ipn(ipnQuery(p, 350_000, "24", "02", "1007")))
		assert.Equal(t, events, e.count(`SELECT count(*) FROM provider_events WHERE provider = 'vnpay'`),
			"a payment already final is answered before anything is recorded")
		assert.Equal(t, domain.StatusSucceeded, e.get(p.ID).Status)
	})
	t.Run("00 failure, reason is the response code", func(t *testing.T) {
		p := e.payment(350_000)
		assert.Equal(t, ack{"00", "Confirm Success"}, e.ipn(ipnQuery(p, 350_000, "24", "02", "0")))
		got := e.get(p.ID)
		assert.Equal(t, domain.StatusFailed, got.Status)
		assert.Equal(t, "24", got.FailureReason)
	})
	t.Run("02 on a duplicate failure IPN, deduped by the event key", func(t *testing.T) {
		p := e.payment(350_000)
		q := ipnQuery(p, 350_000, "24", "02", "0")
		// The key, computed independently: hex(sha256(TxnRef|TransactionNo|ResponseCode|TransactionStatus)).
		sum := sha256.Sum256([]byte(vnpay.TxnRef(p.ID) + "|0|24|02"))
		keyRows := func() int {
			return e.count(`SELECT count(*) FROM provider_events WHERE provider = 'vnpay' AND event_key = $1`, hex.EncodeToString(sum[:]))
		}
		require.Zero(t, keyRows())

		require.Equal(t, ack{"00", "Confirm Success"}, e.ipn(q))
		failed := e.get(p.ID)
		require.Equal(t, domain.StatusFailed, failed.Status)
		require.False(t, failed.Status.Final(), "the redelivery must get past the Final() check")
		require.Equal(t, 1, e.outbox(p), "one PaymentFailed")
		require.Equal(t, 1, keyRows(), "the first delivery records its event key")

		assert.Equal(t, ack{"02", "Order already confirmed"}, e.ipn(q))
		assert.Equal(t, 1, e.outbox(p), "no second PaymentFailed")
		assert.Equal(t, failed.Version, e.get(p.ID).Version)
		assert.Equal(t, 1, keyRows(), "the redelivery hit the existing provider_events key")
	})
	t.Run("response 00 with a non-00 transaction status is a failure", func(t *testing.T) {
		p := e.payment(350_000)
		assert.Equal(t, "00", e.ipn(ipnQuery(p, 350_000, "00", "02", "1008")).code)
		assert.Equal(t, domain.StatusFailed, e.get(p.ID).Status)
	})
	// Status 05 (VNPay processing) and 07 (suspected fraud) mean the money may still move, so the IPN
	// must not settle the payment. It acknowledges receipt and leaves it PENDING for the reconciler.
	for _, txnStatus := range []string{"01", "05", "06", "07"} {
		t.Run("response 00 with status "+txnStatus+" leaves the payment pending", func(t *testing.T) {
			p := e.payment(350_000)
			events := e.count(`SELECT count(*) FROM provider_events WHERE provider = 'vnpay'`)
			assert.Equal(t, ack{"00", "Confirm Success"}, e.ipn(ipnQuery(p, 350_000, "00", txnStatus, "ipn"+txnStatus)))
			got := e.get(p.ID)
			assert.Equal(t, domain.StatusPending, got.Status)
			assert.Equal(t, p.Version, got.Version, "nothing is applied")
			assert.Zero(t, e.outbox(p), "no event for an unsettled observation")
			assert.Equal(t, events, e.count(`SELECT count(*) FROM provider_events WHERE provider = 'vnpay'`),
				"nothing is recorded, so a later real IPN is not taken for a duplicate")
		})
	}
	t.Run("99 unexpected error", func(t *testing.T) {
		p := e.payment(350_000)
		cancelled, cancel := context.WithCancel(bg)
		cancel()
		c, m := e.cb.IPN(cancelled, ipnQuery(p, 350_000, "00", "00", "1009"))
		assert.Equal(t, ack{"99", "Unknown error"}, ack{c, m})
		assert.Equal(t, domain.StatusPending, e.get(p.ID).Status)
	})
	assert.Empty(t, e.api.calls(), "a verified IPN is authoritative; no querydr")
}

func returnQuery(p domain.Payment) url.Values {
	return ipnQuery(p, p.Amount, "00", "00", "2001")
}

func TestReturn(t *testing.T) {
	e := newCBEnv(t)

	t.Run("bad signature changes nothing", func(t *testing.T) {
		p := e.payment(350_000)
		q := returnQuery(p)
		q.Set("vnp_ResponseCode", "24")
		assert.Empty(t, e.cb.Return(bg, q))
		assert.Equal(t, domain.StatusPending, e.get(p.ID).Status)
		assert.Empty(t, e.api.calls())
	})
	t.Run("unknown TxnRef", func(t *testing.T) {
		p := e.payment(350_000)
		q := returnQuery(p)
		q.Set("vnp_TxnRef", vnpay.TxnRef(uid()))
		assert.Empty(t, e.cb.Return(bg, sign(q)))
		assert.Empty(t, e.api.calls())
	})
	t.Run("valid: applies querydr, never the return parameters", func(t *testing.T) {
		p := e.payment(350_000)
		e.api.set("querydr_pending", 0, false)
		assert.Equal(t, p.ID, e.cb.Return(bg, returnQuery(p)))
		assert.Equal(t, domain.StatusPending, e.get(p.ID).Status, "the return says paid, querydr says pending")
		require.Len(t, e.api.calls(), 1)

		e.api.set("querydr_paid", 0, false)
		assert.Equal(t, p.ID, e.cb.Return(bg, returnQuery(p)))
		got := e.get(p.ID)
		assert.Equal(t, domain.StatusSucceeded, got.Status)
		assert.Equal(t, "14226112", got.ProviderTxnID)
	})
	t.Run("query failure still returns the id", func(t *testing.T) {
		p := e.payment(350_000)
		e.api.set("querydr_paid", 0, true)
		assert.Equal(t, p.ID, e.cb.Return(bg, returnQuery(p)))
		assert.Equal(t, domain.StatusPending, e.get(p.ID).Status)
	})
}
