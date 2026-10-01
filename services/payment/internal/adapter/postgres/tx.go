package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/inbox"
	"github.com/ngoxuanchien/e-commerce/pkg/outbox"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// consumerName identifies payment-svc in the processed_events inbox.
const consumerName = "payment-svc"

// txStore implements app.Tx inside one database transaction.
type txStore struct {
	tx pgx.Tx
	q  *sqlcdb.Queries
}

var _ app.Tx = (*txStore)(nil)

func (t *txStore) InsertPayment(ctx context.Context, p app.NewPayment) error {
	ids := make([]uuid.UUID, 3)
	for i, s := range []string{p.ID, p.CheckoutID, p.BuyerID} {
		u, err := mustParseUUID(s)
		if err != nil {
			return err
		}
		ids[i] = u
	}
	if err := t.q.InsertPayment(ctx, sqlcdb.InsertPaymentParams{
		ID: ids[0], CheckoutID: ids[1], BuyerID: ids[2], Method: p.Method, Provider: p.Provider,
		Status: string(p.Status), Amount: p.Amount, Captured: p.Captured, ProviderRef: nullable(p.ProviderRef),
		ProviderTxnID: nullable(p.ProviderTxnID), RedirectUrl: nullable(p.RedirectURL), ExpiresAt: p.ExpiresAt,
		FailureReason: nullable(p.FailureReason), Version: int32(p.Version), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}); err != nil {
		return fmt.Errorf("insert payment: %w", err)
	}
	for _, a := range p.Allocations {
		orderID, err := mustParseUUID(a.OrderID)
		if err != nil {
			return err
		}
		sellerID, err := mustParseUUID(a.SellerUserID)
		if err != nil {
			return err
		}
		if err := t.q.InsertAllocation(ctx, sqlcdb.InsertAllocationParams{
			PaymentID: ids[0], OrderID: orderID, SellerUserID: sellerID, Amount: a.Amount, CommissionAmount: a.CommissionAmount,
		}); err != nil {
			return fmt.Errorf("insert allocation: %w", err)
		}
	}
	return nil
}

func (t *txStore) LockCheckoutPayments(ctx context.Context, checkoutID string) ([]domain.Payment, error) {
	uid, err := mustParseUUID(checkoutID)
	if err != nil {
		return nil, err
	}
	rows, err := t.q.LockCheckoutPayments(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("lock checkout payments: %w", err)
	}
	return toDomainPayments(rows), nil
}

func (t *txStore) ClaimPendingForReconcile(ctx context.Context, now, before, leaseUntil time.Time, limit int) ([]domain.Payment, error) {
	rows, err := t.q.ClaimPendingForReconcile(ctx, sqlcdb.ClaimPendingForReconcileParams{
		LeaseUntil: leaseUntil, Before: before, Now: now, PageLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("claim pending for reconcile: %w", err)
	}
	return toDomainPayments(rows), nil
}

func (t *txStore) UpdatePayment(ctx context.Context, p domain.Payment) error {
	uid, err := mustParseUUID(p.ID)
	if err != nil {
		return err
	}
	n, err := t.q.UpdatePayment(ctx, sqlcdb.UpdatePaymentParams{
		Status: string(p.Status), Captured: p.Captured, ProviderTxnID: nullable(p.ProviderTxnID),
		FailureReason: nullable(p.FailureReason), UpdatedAt: p.UpdatedAt, ID: uid, Version: int32(p.Version),
	})
	if err != nil {
		return fmt.Errorf("update payment: %w", err)
	}
	if n == 0 {
		return app.ErrConcurrentUpdate
	}
	return nil
}

func (t *txStore) SetSession(ctx context.Context, paymentID, providerRef, redirectURL string) error {
	uid, err := mustParseUUID(paymentID)
	if err != nil {
		return err
	}
	n, err := t.q.SetSession(ctx, sqlcdb.SetSessionParams{ProviderRef: nullable(providerRef), RedirectUrl: nullable(redirectURL), ID: uid})
	if err != nil {
		return fmt.Errorf("set session: %w", err)
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (t *txStore) Allocations(ctx context.Context, paymentID string) ([]app.Allocation, error) {
	uid, err := mustParseUUID(paymentID)
	if err != nil {
		return nil, err
	}
	rows, err := t.q.ListAllocations(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list allocations: %w", err)
	}
	out := make([]app.Allocation, len(rows))
	for i, r := range rows {
		out[i] = app.Allocation{OrderID: r.OrderID.String(), SellerUserID: r.SellerUserID.String(), Amount: r.Amount, CommissionAmount: r.CommissionAmount}
	}
	return out, nil
}

func (t *txStore) InsertProviderEvent(ctx context.Context, provider, key string, at time.Time) (bool, error) {
	n, err := t.q.InsertProviderEvent(ctx, sqlcdb.InsertProviderEventParams{Provider: provider, EventKey: key, ReceivedAt: at})
	if err != nil {
		return false, fmt.Errorf("insert provider event: %w", err)
	}
	return n == 1, nil
}

func (t *txStore) RecordCancellation(ctx context.Context, orderID, checkoutID string, at time.Time) (bool, error) {
	oid, err := mustParseUUID(orderID)
	if err != nil {
		return false, err
	}
	cid, err := mustParseUUID(checkoutID)
	if err != nil {
		return false, err
	}
	n, err := t.q.RecordCancellation(ctx, sqlcdb.RecordCancellationParams{OrderID: oid, CheckoutID: cid, CancelledAt: at})
	if err != nil {
		return false, fmt.Errorf("record cancellation: %w", err)
	}
	return n == 1, nil
}

func (t *txStore) IsCancelled(ctx context.Context, orderID string) (bool, error) {
	oid, err := mustParseUUID(orderID)
	if err != nil {
		return false, err
	}
	ok, err := t.q.IsCancelled(ctx, oid)
	if err != nil {
		return false, fmt.Errorf("is cancelled: %w", err)
	}
	return ok, nil
}

func (t *txStore) CapturedAllocationFor(ctx context.Context, orderID string) (domain.Payment, app.Allocation, bool, error) {
	oid, err := mustParseUUID(orderID)
	if err != nil {
		return domain.Payment{}, app.Allocation{}, false, err
	}
	r, err := t.q.CapturedAllocationFor(ctx, oid)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Payment{}, app.Allocation{}, false, nil
	}
	if err != nil {
		return domain.Payment{}, app.Allocation{}, false, fmt.Errorf("captured allocation: %w", err)
	}
	p := toDomainPayment(sqlcdb.Payment{
		ID: r.ID, CheckoutID: r.CheckoutID, BuyerID: r.BuyerID, Method: r.Method, Provider: r.Provider,
		Status: r.Status, Amount: r.Amount, Captured: r.Captured, ProviderRef: r.ProviderRef,
		ProviderTxnID: r.ProviderTxnID, RedirectUrl: r.RedirectUrl, ExpiresAt: r.ExpiresAt,
		FailureReason: r.FailureReason, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	})
	a := app.Allocation{OrderID: r.AllocOrderID.String(), SellerUserID: r.AllocSellerUserID.String(),
		Amount: r.AllocAmount, CommissionAmount: r.AllocCommissionAmount}
	return p, a, true, nil
}

func (t *txStore) InsertRefund(ctx context.Context, r app.Refund) (bool, error) {
	rid, err := mustParseUUID(r.ID)
	if err != nil {
		return false, err
	}
	pid, err := mustParseUUID(r.PaymentID)
	if err != nil {
		return false, err
	}
	var orderID *uuid.UUID
	if r.OrderID != nil {
		u, err := mustParseUUID(*r.OrderID)
		if err != nil {
			return false, err
		}
		orderID = &u
	}
	n, err := t.q.InsertRefund(ctx, sqlcdb.InsertRefundParams{
		ID: rid, PaymentID: pid, OrderID: orderID, Amount: r.Amount, Status: r.Status,
		ProviderRefundRef: nullable(r.ProviderRefundRef), Attempts: int32(r.Attempts), NextAttemptAt: r.NextAttemptAt,
		LastError: nullable(r.LastError), CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return false, fmt.Errorf("insert refund: %w", err)
	}
	return n == 1, nil
}

func (t *txStore) ClaimDueRefunds(ctx context.Context, now time.Time, limit int) ([]app.Refund, error) {
	rows, err := t.q.ClaimDueRefunds(ctx, sqlcdb.ClaimDueRefundsParams{Now: now, PageLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("claim due refunds: %w", err)
	}
	out := make([]app.Refund, len(rows))
	for i, r := range rows {
		out[i] = toRefund(r.ID, r.PaymentID, r.OrderID, r.Amount, r.Status, r.ProviderRefundRef, r.Attempts,
			r.NextAttemptAt, r.LastError, r.CreatedAt, r.CheckoutID, r.Provider)
	}
	return out, nil
}

func (t *txStore) LockRefund(ctx context.Context, id string) (app.Refund, error) {
	rid, err := uuid.Parse(id)
	if err != nil {
		return app.Refund{}, app.ErrNotFound
	}
	r, err := t.q.LockRefund(ctx, rid)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Refund{}, app.ErrNotFound
	}
	if err != nil {
		return app.Refund{}, fmt.Errorf("lock refund: %w", err)
	}
	return toRefund(r.ID, r.PaymentID, r.OrderID, r.Amount, r.Status, r.ProviderRefundRef, r.Attempts,
		r.NextAttemptAt, r.LastError, r.CreatedAt, r.CheckoutID, r.Provider), nil
}

func (t *txStore) UpdateRefund(ctx context.Context, r app.Refund) error {
	rid, err := mustParseUUID(r.ID)
	if err != nil {
		return err
	}
	n, err := t.q.UpdateRefund(ctx, sqlcdb.UpdateRefundParams{
		Status: r.Status, ProviderRefundRef: nullable(r.ProviderRefundRef), Attempts: int32(r.Attempts),
		NextAttemptAt: r.NextAttemptAt, LastError: nullable(r.LastError), UpdatedAt: time.Now().UTC(), ID: rid,
	})
	if err != nil {
		return fmt.Errorf("update refund: %w", err)
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (t *txStore) Publish(ctx context.Context, key string, p event.Payload, at time.Time) error {
	b, err := event.Encode(p, at)
	if err != nil {
		return err
	}
	return outbox.Write(ctx, t.tx, outbox.Message{Topic: event.TopicPaymentEvents, Key: key, Payload: b})
}

func (t *txStore) MarkProcessed(ctx context.Context, eventID string) (bool, error) {
	return inbox.MarkProcessed(ctx, t.tx, consumerName, eventID)
}
