package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ngoxuanchien/e-commerce/pkg/inbox"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

func (t *txStore) MarkProcessed(ctx context.Context, eventID uuid.UUID) (bool, error) {
	return inbox.MarkProcessed(ctx, t.tx, app.ConsumerName, eventID.String())
}

func (t *txStore) InsertAllocation(ctx context.Context, a domain.Allocation) error {
	if err := t.q.InsertAllocation(ctx, sqlcdb.InsertAllocationParams{
		OrderID: a.OrderID, PaymentID: a.PaymentID, CheckoutID: a.CheckoutID, SellerUserID: a.SellerUserID,
		Amount: a.Amount, CommissionAmount: a.Commission, Provider: a.Provider,
	}); err != nil {
		return fmt.Errorf("insert allocation: %w", err)
	}
	return nil
}

func (t *txStore) GetAllocation(ctx context.Context, orderID uuid.UUID) (domain.Allocation, bool, error) {
	r, err := t.q.GetAllocation(ctx, orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Allocation{}, false, nil
	}
	if err != nil {
		return domain.Allocation{}, false, fmt.Errorf("get allocation: %w", err)
	}
	return domain.Allocation{
		OrderID: r.OrderID, PaymentID: r.PaymentID, CheckoutID: r.CheckoutID, SellerUserID: r.SellerUserID,
		Amount: r.Amount, Commission: r.CommissionAmount, Provider: r.Provider,
	}, true, nil
}

func (t *txStore) InsertPending(ctx context.Context, orderID uuid.UUID, envelope []byte, at time.Time) error {
	if err := t.q.InsertPending(ctx, sqlcdb.InsertPendingParams{OrderID: orderID, Event: envelope, CreatedAt: at}); err != nil {
		return fmt.Errorf("insert pending posting: %w", err)
	}
	return nil
}

func toPending(rows []sqlcdb.PendingPosting) []app.PendingPosting {
	out := make([]app.PendingPosting, len(rows))
	for i, r := range rows {
		out[i] = app.PendingPosting{OrderID: r.OrderID, Envelope: r.Event, Attempts: int(r.Attempts), CreatedAt: r.CreatedAt}
	}
	return out
}

func (t *txStore) TakePending(ctx context.Context, orderIDs []uuid.UUID) ([]app.PendingPosting, error) {
	rows, err := t.q.TakePending(ctx, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("take pending postings: %w", err)
	}
	return toPending(rows), nil
}

func (t *txStore) ClaimPending(ctx context.Context, limit int) ([]app.PendingPosting, error) {
	rows, err := t.q.ClaimPending(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("claim pending postings: %w", err)
	}
	return toPending(rows), nil
}

func (t *txStore) DeletePending(ctx context.Context, orderID uuid.UUID) error {
	if err := t.q.DeletePending(ctx, orderID); err != nil {
		return fmt.Errorf("delete pending posting: %w", err)
	}
	return nil
}

func (t *txStore) TouchPending(ctx context.Context, orderID uuid.UUID, at time.Time) error {
	if err := t.q.TouchPending(ctx, sqlcdb.TouchPendingParams{OrderID: orderID, At: &at}); err != nil {
		return fmt.Errorf("touch pending posting: %w", err)
	}
	return nil
}
