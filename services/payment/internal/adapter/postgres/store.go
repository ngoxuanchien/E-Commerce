// Package postgres implements app.Store on PostgreSQL with sqlc-generated
// queries (queries/ → sqlcdb/).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/pkg/idemkey"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// Store implements app.Store.
type Store struct {
	pool *pgxpool.Pool
	q    *sqlcdb.Queries
}

var _ app.Store = (*Store)(nil)

// NewStore uses pool, whose database must have the service migrations applied.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: sqlcdb.New(pool)}
}

func (s *Store) InTx(ctx context.Context, fn func(app.Tx) error) error {
	return pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(&txStore{tx: tx, q: s.q.WithTx(tx)})
	})
}

func (s *Store) GetPayment(ctx context.Context, id string) (domain.Payment, error) {
	uid, ok := parseUUID(id)
	if !ok {
		return domain.Payment{}, app.ErrNotFound
	}
	row, err := s.q.GetPayment(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Payment{}, app.ErrNotFound
	}
	if err != nil {
		return domain.Payment{}, fmt.Errorf("get payment: %w", err)
	}
	return toDomainPayment(row), nil
}

func (s *Store) GetPaymentByProviderRef(ctx context.Context, provider, ref string) (domain.Payment, error) {
	row, err := s.q.GetPaymentByProviderRef(ctx, sqlcdb.GetPaymentByProviderRefParams{Provider: provider, ProviderRef: &ref})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Payment{}, app.ErrNotFound
	}
	if err != nil {
		return domain.Payment{}, fmt.Errorf("get payment by provider ref: %w", err)
	}
	return toDomainPayment(row), nil
}

func (s *Store) ListCheckoutPayments(ctx context.Context, checkoutID string) ([]domain.Payment, error) {
	uid, ok := parseUUID(checkoutID)
	if !ok {
		return []domain.Payment{}, nil
	}
	rows, err := s.q.ListCheckoutPayments(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list checkout payments: %w", err)
	}
	return toDomainPayments(rows), nil
}

func (s *Store) ListPendingOlderThan(ctx context.Context, before time.Time, limit int) ([]domain.Payment, error) {
	rows, err := s.q.ListPendingOlderThan(ctx, sqlcdb.ListPendingOlderThanParams{Before: before, PageLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list pending payments: %w", err)
	}
	return toDomainPayments(rows), nil
}

func (s *Store) GetRefundByProviderRef(ctx context.Context, provider, ref string) (app.Refund, error) {
	r, err := s.q.GetRefundByProviderRef(ctx, sqlcdb.GetRefundByProviderRefParams{Provider: provider, ProviderRefundRef: &ref})
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Refund{}, app.ErrNotFound
	}
	if err != nil {
		return app.Refund{}, fmt.Errorf("get refund by provider ref: %w", err)
	}
	return toRefund(r.ID, r.PaymentID, r.OrderID, r.Amount, r.Status, r.ProviderRefundRef, r.Attempts,
		r.NextAttemptAt, r.LastError, r.CheckoutID, r.Provider), nil
}

func (s *Store) PurgeProviderEventsBefore(ctx context.Context, t time.Time) (int64, error) {
	n, err := s.q.PurgeProviderEventsBefore(ctx, t)
	if err != nil {
		return 0, fmt.Errorf("purge provider events: %w", err)
	}
	return n, nil
}

func (s *Store) PurgeIdempotencyKeysBefore(ctx context.Context, t time.Time) (int64, error) {
	return idemkey.Purge(ctx, s.pool, t)
}
