package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

// ErrWouldOverdraw: the posting would leave a liability account in debit (invariant 3).
var ErrWouldOverdraw = errors.New("ledger: posting would put a liability account into debit")

// OverdrawError details ErrWouldOverdraw. Balance is the account's Σ before
// the posting; Debit is the posting's net debit to it.
type OverdrawError struct {
	Account string
	Balance int64
	Debit   int64
}

func (e *OverdrawError) Error() string {
	return fmt.Sprintf("%s: %s has balance %d, debit %d", ErrWouldOverdraw, e.Account, e.Balance, e.Debit)
}

func (e *OverdrawError) Is(target error) bool { return target == ErrWouldOverdraw }

// PostResult is the posted transaction. Replayed: its key was already
// posted, and nothing new was written.
type PostResult struct {
	TransactionID uuid.UUID
	Replayed      bool
}

// Post applies j inside the caller's transaction (WS2' §4):
//  1. validate j (Σ = 0, at least 2 lines);
//  2. create its accounts and lock the liability accounts it debits, in id order;
//  3. if its idempotency key was already posted, return that transaction;
//  4. refuse it if a locked account would end in debit;
//  5. insert it. A concurrent poster of the same key that committed first
//     turns this into a replay.
func Post(ctx context.Context, l Ledger, j domain.Journal) (PostResult, error) {
	if err := j.Validate(); err != nil {
		return PostResult{}, err
	}
	if j.OccurredAt.IsZero() {
		j.OccurredAt = time.Now().UTC()
	}
	accounts := make([]domain.Account, len(j.Lines))
	for i, line := range j.Lines {
		accounts[i] = line.Account
	}
	ids, err := l.EnsureAccounts(ctx, accounts)
	if err != nil {
		return PostResult{}, err
	}
	debits := j.LiabilityDebits()
	lock := make([]uuid.UUID, 0, len(debits))
	for _, d := range debits {
		lock = append(lock, ids[d.Account.Code])
	}
	// Byte order is Postgres's uuid order (LockAccounts' ORDER BY id); comparing
	// uuid.String() instead would disagree with it and reintroduce deadlocks.
	slices.SortFunc(lock, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	if err := l.LockAccounts(ctx, lock); err != nil {
		return PostResult{}, err
	}
	if id, found, err := l.TransactionIDByKey(ctx, j.Key); err != nil {
		return PostResult{}, err
	} else if found {
		return PostResult{TransactionID: id, Replayed: true}, nil
	}
	for _, d := range debits {
		bal, err := l.Balance(ctx, ids[d.Account.Code])
		if err != nil {
			return PostResult{}, err
		}
		if bal+d.Amount > 0 {
			return PostResult{}, &OverdrawError{Account: d.Account.Code, Balance: bal, Debit: d.Amount}
		}
	}
	id := uuid.Must(uuid.NewV7())
	inserted, err := l.InsertJournal(ctx, id, j, ids)
	if err != nil {
		return PostResult{}, err
	}
	if !inserted {
		existing, found, err := l.TransactionIDByKey(ctx, j.Key)
		if err != nil {
			return PostResult{}, err
		}
		if !found {
			return PostResult{}, fmt.Errorf("ledger: key %s conflicted but no transaction was found", j.Key)
		}
		return PostResult{TransactionID: existing, Replayed: true}, nil
	}
	return PostResult{TransactionID: id}, nil
}
