// Package app holds ledger-svc's use cases: the posting engine, the event
// handlers, payouts and the background jobs. Persistence is behind Store/Tx.
package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

// Store is the database. InTx runs fn in one transaction, committing on nil.
type Store interface {
	InTx(ctx context.Context, fn func(Tx) error) error
}

// Tx is everything a use case may do inside one database transaction.
type Tx interface {
	Ledger

	// MarkProcessed records the event in processed_events; false = seen before.
	MarkProcessed(ctx context.Context, eventID uuid.UUID) (bool, error)
	// InsertAllocation writes an escrow allocation; an existing row is kept.
	InsertAllocation(ctx context.Context, a domain.Allocation) error
	GetAllocation(ctx context.Context, orderID uuid.UUID) (domain.Allocation, bool, error)

	// InsertPending parks an OrderCompleted envelope until its capture; an existing row is kept.
	InsertPending(ctx context.Context, orderID uuid.UUID, envelope []byte, at time.Time) error
	// TakePending locks the pending rows of these orders (waits for other lockers).
	TakePending(ctx context.Context, orderIDs []uuid.UUID) ([]PendingPosting, error)
	// ClaimPending locks up to limit pending rows, oldest first, skipping locked ones.
	ClaimPending(ctx context.Context, limit int) ([]PendingPosting, error)
	DeletePending(ctx context.Context, orderID uuid.UUID) error
	// TouchPending counts one more attempt at `at`.
	TouchPending(ctx context.Context, orderID uuid.UUID, at time.Time) error
}

// PendingPosting is an OrderCompleted that arrived before its capture.
type PendingPosting struct {
	OrderID   uuid.UUID
	Envelope  []byte // the event.Envelope JSON
	Attempts  int
	CreatedAt time.Time
}

// Ledger is the posting primitives Post is built from.
type Ledger interface {
	// EnsureAccounts creates missing accounts and returns code → id for all of them.
	EnsureAccounts(ctx context.Context, accounts []domain.Account) (map[string]uuid.UUID, error)
	// LockAccounts takes FOR UPDATE row locks in id order.
	LockAccounts(ctx context.Context, ids []uuid.UUID) error
	// Balance is Σ entries.amount of one account.
	Balance(ctx context.Context, accountID uuid.UUID) (int64, error)
	// TransactionIDByKey finds an already posted transaction.
	TransactionIDByKey(ctx context.Context, key string) (uuid.UUID, bool, error)
	// InsertJournal inserts the transaction and its entries; false when the
	// key already exists (nothing is inserted).
	InsertJournal(ctx context.Context, id uuid.UUID, j domain.Journal, accountIDs map[string]uuid.UUID) (bool, error)
}
