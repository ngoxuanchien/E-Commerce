package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/testsupport"
)

var ctx = context.Background()

func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// rawAccount inserts an account of the given type and returns its id.
func rawAccount(t *testing.T, db *pgxpool.Pool, typ string) uuid.UUID {
	t.Helper()
	id := newID()
	_, err := db.Exec(ctx, `INSERT INTO accounts (id, code, type) VALUES ($1, $2, $3)`, id, "t:"+id.String(), typ)
	require.NoError(t, err)
	return id
}

type rawLine struct {
	account uuid.UUID
	amount  int64
}

// rawTx inserts one journal transaction with the given entries in its own
// database transaction, bypassing the app, and returns the commit error.
func rawTx(db *pgxpool.Pool, key string, lines ...rawLine) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txID := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO journal_transactions (id, idempotency_key, kind, occurred_at)
		VALUES ($1, $2, 'PAY', now())`, txID, key); err != nil {
		return err
	}
	for _, l := range lines {
		if _, err := tx.Exec(ctx, `INSERT INTO entries (id, transaction_id, account_id, amount) VALUES ($1, $2, $3, $4)`,
			newID(), txID, l.account, l.amount); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

const (
	checkViolation    = "23514"
	restrictViolation = "23001"
	uniqueViolation   = "23505"
)

func TestBalancedTransactionCommits(t *testing.T) {
	db := testsupport.DB(t)
	a, b := rawAccount(t, db, "asset"), rawAccount(t, db, "revenue")
	require.NoError(t, rawTx(db, "k1", rawLine{a, 100}, rawLine{b, -100}))
}

func TestUnbalancedTransactionFailsAtCommit(t *testing.T) {
	db := testsupport.DB(t)
	a, b := rawAccount(t, db, "asset"), rawAccount(t, db, "revenue")
	err := rawTx(db, "k1", rawLine{a, 100}, rawLine{b, -99})
	assert.Equal(t, checkViolation, pgCode(err), "%v", err)
}

func TestSingleAndZeroEntryTransactionsFail(t *testing.T) {
	db := testsupport.DB(t)
	a := rawAccount(t, db, "asset")
	assert.Equal(t, checkViolation, pgCode(rawTx(db, "k1", rawLine{a, 100})))
	assert.Equal(t, checkViolation, pgCode(rawTx(db, "k2")))
}

func TestZeroAmountEntryRejected(t *testing.T) {
	db := testsupport.DB(t)
	a, b := rawAccount(t, db, "asset"), rawAccount(t, db, "revenue")
	assert.Equal(t, checkViolation, pgCode(rawTx(db, "k1", rawLine{a, 0}, rawLine{b, 0})))
}

func TestLiabilityCannotEndInDebit(t *testing.T) {
	db := testsupport.DB(t)
	liab, cash := rawAccount(t, db, "liability"), rawAccount(t, db, "asset")
	assert.Equal(t, checkViolation, pgCode(rawTx(db, "k1", rawLine{liab, 100}, rawLine{cash, -100})))
	// Crediting it first makes the same debit legal.
	require.NoError(t, rawTx(db, "k2", rawLine{cash, 100}, rawLine{liab, -100}))
	require.NoError(t, rawTx(db, "k3", rawLine{liab, 100}, rawLine{cash, -100}))
}

func TestJournalIsAppendOnly(t *testing.T) {
	db := testsupport.DB(t)
	a, b := rawAccount(t, db, "asset"), rawAccount(t, db, "revenue")
	require.NoError(t, rawTx(db, "k1", rawLine{a, 100}, rawLine{b, -100}))
	_, err := db.Exec(ctx, `INSERT INTO escrow_allocations (order_id, payment_id, checkout_id, seller_user_id, amount, commission_amount, provider)
		VALUES ($1, $2, $3, $4, 100, 5, 'fake')`, newID(), newID(), newID(), newID())
	require.NoError(t, err)

	for _, stmt := range []string{
		`UPDATE entries SET amount = amount * 2`,
		`DELETE FROM entries`,
		`TRUNCATE entries`,
		`UPDATE journal_transactions SET kind = 'REFUND'`,
		`DELETE FROM journal_transactions`,
		`TRUNCATE journal_transactions CASCADE`,
		`UPDATE escrow_allocations SET amount = 1`,
		`DELETE FROM escrow_allocations`,
		`TRUNCATE escrow_allocations`,
	} {
		_, err := db.Exec(ctx, stmt)
		assert.Equal(t, restrictViolation, pgCode(err), "%q: %v", stmt, err)
	}
	var n int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM entries`).Scan(&n))
	assert.Equal(t, 2, n)
}

func TestIdempotencyKeyIsUnique(t *testing.T) {
	db := testsupport.DB(t)
	a, b := rawAccount(t, db, "asset"), rawAccount(t, db, "revenue")
	require.NoError(t, rawTx(db, "same", rawLine{a, 1}, rawLine{b, -1}))
	err := rawTx(db, "same", rawLine{a, 1}, rawLine{b, -1})
	assert.Equal(t, uniqueViolation, pgCode(err))
}
