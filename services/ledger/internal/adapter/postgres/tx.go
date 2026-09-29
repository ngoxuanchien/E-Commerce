package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

// txStore implements app.Tx inside one database transaction.
type txStore struct {
	tx pgx.Tx
	q  *sqlcdb.Queries
}

var _ app.Tx = (*txStore)(nil)

func newUUID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func (t *txStore) EnsureAccounts(ctx context.Context, accounts []domain.Account) (map[string]uuid.UUID, error) {
	codes := make([]string, 0, len(accounts))
	seen := map[string]bool{}
	for _, a := range accounts {
		if seen[a.Code] {
			continue
		}
		seen[a.Code] = true
		codes = append(codes, a.Code)
		if err := t.q.EnsureAccount(ctx, sqlcdb.EnsureAccountParams{
			ID: newUUID(), Code: a.Code, Type: string(a.Type), OwnerUserID: a.OwnerID, OrderID: a.OrderID,
		}); err != nil {
			return nil, fmt.Errorf("ensure account %s: %w", a.Code, err)
		}
	}
	rows, err := t.q.AccountIDsByCode(ctx, codes)
	if err != nil {
		return nil, fmt.Errorf("account ids: %w", err)
	}
	ids := make(map[string]uuid.UUID, len(rows))
	for _, r := range rows {
		ids[r.Code] = r.ID
	}
	if len(ids) != len(codes) {
		return nil, fmt.Errorf("account ids: found %d of %d accounts", len(ids), len(codes))
	}
	return ids, nil
}

func (t *txStore) LockAccounts(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := t.q.LockAccounts(ctx, ids); err != nil {
		return fmt.Errorf("lock accounts: %w", err)
	}
	return nil
}

func (t *txStore) Balance(ctx context.Context, accountID uuid.UUID) (int64, error) {
	b, err := t.q.AccountBalance(ctx, accountID)
	if err != nil {
		return 0, fmt.Errorf("account balance: %w", err)
	}
	return b, nil
}

func (t *txStore) TransactionIDByKey(ctx context.Context, key string) (uuid.UUID, bool, error) {
	id, err := t.q.TransactionIDByKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("transaction by key: %w", err)
	}
	return id, true, nil
}

func (t *txStore) InsertJournal(ctx context.Context, id uuid.UUID, j domain.Journal, accountIDs map[string]uuid.UUID) (bool, error) {
	refs, err := json.Marshal(j.Refs)
	if err != nil {
		return false, fmt.Errorf("encode refs: %w", err)
	}
	n, err := t.q.InsertTransaction(ctx, sqlcdb.InsertTransactionParams{
		ID: id, IdempotencyKey: j.Key, Kind: string(j.Kind), Refs: refs,
		SourceEventID: j.SourceEventID, OccurredAt: j.OccurredAt,
	})
	if err != nil {
		return false, fmt.Errorf("insert transaction: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	for _, l := range j.Lines {
		if err := t.q.InsertEntry(ctx, sqlcdb.InsertEntryParams{
			ID: newUUID(), TransactionID: id, AccountID: accountIDs[l.Account.Code], Amount: l.Amount,
		}); err != nil {
			return false, fmt.Errorf("insert entry: %w", err)
		}
	}
	return true, nil
}
