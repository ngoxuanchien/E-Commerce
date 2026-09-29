// Package app holds ledger-svc's use cases: the posting engine, the event
// handlers, payouts and the background jobs. Persistence is behind Store/Tx.
package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/idemkey"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

// Store is the database. InTx runs fn in one transaction, committing on nil.
type Store interface {
	InTx(ctx context.Context, fn func(Tx) error) error

	// ClaimKey claims an Idempotency-Key outside any business transaction (pkg/idemkey).
	ClaimKey(ctx context.Context, scope string, userID uuid.UUID, key string, requestHash []byte, at, staleBefore time.Time) (idemkey.Record, bool, error)
	// ReleaseKey frees a claim after a transient failure.
	ReleaseKey(ctx context.Context, scope string, userID uuid.UUID, key string, claimedAt time.Time) error

	// WalletBalances are the raw Σ of the seller's wallet, payout-pending and escrow accounts.
	WalletBalances(ctx context.Context, seller uuid.UUID) (WalletBalances, error)
	// ListWalletEntries returns wallet entries newest first, strictly before `before`;
	// Amount is the raw entry amount (credit < 0).
	ListWalletEntries(ctx context.Context, seller uuid.UUID, before *Cursor, limit int) ([]WalletEntry, error)
	ListPayouts(ctx context.Context, seller uuid.UUID, status *domain.PayoutStatus, before *Cursor, limit int) ([]domain.Payout, error)
	TrialBalance(ctx context.Context) (TrialBalance, error)
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

	// CompleteKey stores the final response of a claimed key (idemkey.ErrClaimLost if reclaimed).
	CompleteKey(ctx context.Context, scope string, userID uuid.UUID, key string, claimedAt time.Time, resp Response) error

	InsertPayout(ctx context.Context, p domain.Payout) error
	// ClaimDuePayout locks the oldest REQUESTED payout requested at or before due, skipping locked ones.
	ClaimDuePayout(ctx context.Context, due time.Time) (domain.Payout, bool, error)
	// FinishPayout stores p's final status if the row is still at prevVersion.
	FinishPayout(ctx context.Context, p domain.Payout, prevVersion int) error
	// PublishPayoutEvent writes p to the outbox for payout.events, keyed by seller.
	PublishPayoutEvent(ctx context.Context, sellerID uuid.UUID, p event.Payload, at time.Time) error
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

// WalletBalances are raw account sums (credit-normal: ≤ 0 when healthy).
type WalletBalances struct {
	Wallet        int64
	PayoutPending int64
	Escrow        int64
}

// WalletEntry is one entry of a seller wallet joined with its transaction.
type WalletEntry struct {
	ID            uuid.UUID
	TransactionID uuid.UUID
	Kind          domain.Kind
	Amount        int64
	OrderID       *uuid.UUID
	PayoutID      *uuid.UUID
	CreatedAt     time.Time
}
