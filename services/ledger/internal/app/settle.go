package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

// Bank transfers a payout to the seller. Transfer must be idempotent per
// payoutID: a crash after it succeeds but before the settle transaction
// commits repeats the call on the next run.
type Bank interface {
	Transfer(ctx context.Context, payoutID, sellerID uuid.UUID, amount int64) error
}

// TransferRejectedError is a final refusal; other Bank errors are retried.
type TransferRejectedError struct{ Reason string }

func (e *TransferRejectedError) Error() string { return "bank rejected the transfer: " + e.Reason }

// SettleConfig tunes SettleJob. Zero BatchSize = 20.
type SettleConfig struct {
	Interval  time.Duration // PAYOUT_SETTLE_INTERVAL
	Delay     time.Duration // PAYOUT_SETTLE_DELAY
	BatchSize int
	Now       func() time.Time
	Logger    *slog.Logger
}

// SettleJob settles REQUESTED payouts older than Delay (WS2' §7).
type SettleJob struct {
	store Store
	bank  Bank
	cfg   SettleConfig
}

func NewSettleJob(store Store, bank Bank, cfg SettleConfig) *SettleJob {
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 20
	}
	return &SettleJob{store: store, bank: bank, cfg: cfg}
}

// Run calls RunOnce every Interval until ctx is cancelled.
func (j *SettleJob) Run(ctx context.Context) error {
	for {
		if _, err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
			j.cfg.Logger.ErrorContext(ctx, "payout settle run failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(j.cfg.Interval):
		}
	}
}

// RunOnce settles up to BatchSize due payouts, one transaction each, and
// returns how many were settled or failed. It stops at the first error.
func (j *SettleJob) RunOnce(ctx context.Context) (int, error) {
	n := 0
	for n < j.cfg.BatchSize {
		done, err := j.settleOne(ctx)
		if err != nil {
			return n, err
		}
		if !done {
			break
		}
		n++
	}
	return n, nil
}

func (j *SettleJob) settleOne(ctx context.Context) (bool, error) {
	var found bool
	err := j.store.InTx(ctx, func(tx Tx) error {
		now := j.cfg.Now()
		p, ok, err := tx.ClaimDuePayout(ctx, now.Add(-j.cfg.Delay))
		if err != nil || !ok {
			return err
		}
		found = true
		prev := p.Version
		var journal domain.Journal
		var ev event.Payload
		terr := j.bank.Transfer(ctx, p.ID, p.SellerUserID, p.Amount)
		var rejected *TransferRejectedError
		switch {
		case terr == nil:
			p.Status, p.SettledAt = domain.PayoutSettled, &now
			journal, err = domain.PayoutSettle(p)
			ev = event.PayoutSettled{PayoutID: p.ID.String(), SellerUserID: p.SellerUserID.String(), Amount: p.Amount, SettledAt: now}
		case errors.As(terr, &rejected):
			reason := rejected.Reason
			p.Status, p.FailureReason = domain.PayoutFailed, &reason
			journal, err = domain.PayoutFail(p)
			ev = event.PayoutFailed{PayoutID: p.ID.String(), SellerUserID: p.SellerUserID.String(), Amount: p.Amount, Reason: reason}
		default:
			return fmt.Errorf("transfer payout %s: %w", p.ID, terr)
		}
		if err != nil {
			return err
		}
		journal.OccurredAt = now
		if _, err := Post(ctx, tx, journal); err != nil {
			return err
		}
		p.Version = prev + 1
		if err := tx.FinishPayout(ctx, p, prev); err != nil {
			return err
		}
		return tx.PublishPayoutEvent(ctx, p.SellerUserID, ev, now)
	})
	return found, err
}
