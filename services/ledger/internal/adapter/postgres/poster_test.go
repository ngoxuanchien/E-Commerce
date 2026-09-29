package postgres_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/testsupport"
)

func must(j domain.Journal, err error) domain.Journal {
	if err != nil {
		panic(err)
	}
	j.OccurredAt = time.Now().UTC()
	return j
}

func post(store *postgres.Store, j domain.Journal) (app.PostResult, error) {
	var res app.PostResult
	err := store.InTx(ctx, func(tx app.Tx) error {
		var err error
		res, err = app.Post(ctx, tx, j)
		return err
	})
	return res, err
}

func balanceOf(t *testing.T, db *pgxpool.Pool, code string) int64 {
	t.Helper()
	var b int64
	require.NoError(t, db.QueryRow(ctx, `SELECT coalesce(sum(e.amount), 0)::bigint
		FROM entries e JOIN accounts a ON a.id = e.account_id WHERE a.code = $1`, code).Scan(&b))
	return b
}

func countRows(t *testing.T, db *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n))
	return n
}

// captured posts PAY + CAPTURE for one order and returns its allocation.
func captured(t *testing.T, store *postgres.Store, amount, commission int64) domain.Allocation {
	t.Helper()
	a := domain.Allocation{OrderID: newID(), PaymentID: newID(), CheckoutID: newID(), SellerUserID: newID(),
		Amount: amount, Commission: commission, Provider: "fake"}
	_, err := post(store, must(domain.Pay(a.PaymentID, a.CheckoutID, a.Provider, amount)))
	require.NoError(t, err)
	_, err = post(store, must(domain.Capture(a.PaymentID, a.CheckoutID, amount, []domain.Allocation{a})))
	require.NoError(t, err)
	return a
}

func TestPostPayAndCaptureCreatesAccountsLazily(t *testing.T) {
	db := testsupport.DB(t)
	store := postgres.NewStore(db)
	a := captured(t, store, 230000, 10000)

	assert.Equal(t, int64(230000), balanceOf(t, db, "psp_clearing:fake"))
	assert.Zero(t, balanceOf(t, db, "checkout_suspense:"+a.CheckoutID.String()))
	assert.Equal(t, int64(-230000), balanceOf(t, db, "escrow:"+a.OrderID.String()))

	var owner, order uuid.UUID
	var typ string
	require.NoError(t, db.QueryRow(ctx, `SELECT type, owner_user_id, order_id FROM accounts WHERE code = $1`,
		"escrow:"+a.OrderID.String()).Scan(&typ, &owner, &order))
	assert.Equal(t, "liability", typ)
	assert.Equal(t, a.SellerUserID, owner)
	assert.Equal(t, a.OrderID, order)
}

func TestPostSameKeyTwiceIsReplay(t *testing.T) {
	db := testsupport.DB(t)
	store := postgres.NewStore(db)
	j := must(domain.Pay(newID(), newID(), "vnpay", 1000))
	first, err := post(store, j)
	require.NoError(t, err)
	assert.False(t, first.Replayed)
	second, err := post(store, j)
	require.NoError(t, err)
	assert.True(t, second.Replayed)
	assert.Equal(t, first.TransactionID, second.TransactionID)
	assert.Equal(t, 1, countRows(t, db, "journal_transactions"))
	assert.Equal(t, 2, countRows(t, db, "entries"))
}

// A replayed RELEASE must be recognised before the overdraw check: the escrow
// is already 0, so checking the balance first would wrongly reject it.
func TestReplayOfDebitingPostingIsNotOverdraw(t *testing.T) {
	store := postgres.NewStore(testsupport.DB(t))
	a := captured(t, store, 100000, 5000)
	rel := must(domain.Release(a))
	_, err := post(store, rel)
	require.NoError(t, err)
	res, err := post(store, rel)
	require.NoError(t, err)
	assert.True(t, res.Replayed)
}

func TestPostRejectsOverdraw(t *testing.T) {
	db := testsupport.DB(t)
	store := postgres.NewStore(db)
	a := domain.Allocation{OrderID: newID(), PaymentID: newID(), CheckoutID: newID(), SellerUserID: newID(), Amount: 100, Commission: 5, Provider: "fake"}
	_, err := post(store, must(domain.Release(a))) // nothing was captured
	require.ErrorIs(t, err, app.ErrWouldOverdraw)
	var oe *app.OverdrawError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, "escrow:"+a.OrderID.String(), oe.Account)
	assert.Equal(t, int64(0), oe.Balance)
	assert.Equal(t, int64(100), oe.Debit)
	assert.Zero(t, countRows(t, db, "journal_transactions"))
}

func TestPostRejectsInvalidJournal(t *testing.T) {
	db := testsupport.DB(t)
	store := postgres.NewStore(db)
	j := must(domain.Pay(newID(), newID(), "fake", 100))
	j.Lines[0].Amount = 101
	_, err := post(store, j)
	require.ErrorIs(t, err, domain.ErrUnbalanced)
	assert.Zero(t, countRows(t, db, "accounts"))
}

func TestConcurrentDebitsOfOneWalletSerialize(t *testing.T) {
	db := testsupport.DB(t)
	store := postgres.NewStore(db)
	a := captured(t, store, 100000, 0)
	_, err := post(store, must(domain.Release(a))) // wallet: −100000
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make([]error, 10)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := domain.Payout{ID: newID(), SellerUserID: a.SellerUserID, Amount: 20000}
			_, results[i] = post(store, must(domain.PayoutRequest(p)))
		}()
	}
	wg.Wait()
	var ok, rejected int
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case assert.ErrorIs(t, err, app.ErrWouldOverdraw):
			rejected++
		}
	}
	assert.Equal(t, 5, ok)
	assert.Equal(t, 5, rejected)
	assert.Zero(t, balanceOf(t, db, "seller_wallet:"+a.SellerUserID.String()))
}

func TestConcurrentPostsOfOneKeyInsertOnce(t *testing.T) {
	db := testsupport.DB(t)
	store := postgres.NewStore(db)
	j := must(domain.Pay(newID(), newID(), "stripe", 500))
	var wg sync.WaitGroup
	var mu sync.Mutex
	fresh := 0
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := post(store, j)
			assert.NoError(t, err)
			if err == nil && !res.Replayed {
				mu.Lock()
				fresh++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, fresh)
	assert.Equal(t, 1, countRows(t, db, "journal_transactions"))
}
