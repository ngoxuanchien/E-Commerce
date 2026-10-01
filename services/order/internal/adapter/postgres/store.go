// Package postgres implements app.Store on PostgreSQL with sqlc-generated
// queries (queries/ → sqlcdb/).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
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

func (s *Store) GetOrder(ctx context.Context, id string) (domain.Order, error) {
	uid, ok := parseUUID(id)
	if !ok {
		return domain.Order{}, app.ErrNotFound
	}
	row, err := s.q.GetOrder(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, app.ErrNotFound
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("get order: %w", err)
	}
	orders, err := s.hydrate(ctx, []sqlcdb.Order{row})
	if err != nil {
		return domain.Order{}, err
	}
	return orders[0], nil
}

func (s *Store) ListOrders(ctx context.Context, q app.ListQuery) ([]domain.Order, error) {
	userID, ok := parseUUID(q.UserID)
	if !ok {
		return []domain.Order{}, nil
	}
	var status *string
	if q.Status != nil {
		v := string(*q.Status)
		status = &v
	}
	var beforeAt *time.Time
	var beforeID *uuid.UUID
	if q.After != nil {
		cid, ok := parseUUID(q.After.ID)
		if !ok {
			return nil, app.ErrInvalidCursor
		}
		at := q.After.CreatedAt
		beforeAt, beforeID = &at, &cid
	}
	var rows []sqlcdb.Order
	var err error
	switch q.Party {
	case app.PartyBuyer:
		rows, err = s.q.ListBuyerOrders(ctx, sqlcdb.ListBuyerOrdersParams{
			BuyerID: userID, Status: status, BeforeCreatedAt: beforeAt, BeforeID: beforeID, PageLimit: int32(q.Limit),
		})
	case app.PartySeller:
		rows, err = s.q.ListSellerOrders(ctx, sqlcdb.ListSellerOrdersParams{
			SellerUserID: userID, Status: status, BeforeCreatedAt: beforeAt, BeforeID: beforeID, PageLimit: int32(q.Limit),
		})
	default:
		return nil, fmt.Errorf("list orders: unknown party %d", q.Party)
	}
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return s.hydrate(ctx, rows)
}

// ListCheckoutOrders returns every order of a checkout, in any status,
// hydrated with items and history and ordered by id. An unknown or malformed
// checkoutID gives an empty slice.
func (s *Store) ListCheckoutOrders(ctx context.Context, checkoutID string) ([]domain.Order, error) {
	uid, ok := parseUUID(checkoutID)
	if !ok {
		return []domain.Order{}, nil
	}
	rows, err := s.q.ListCheckoutOrders(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list checkout orders: %w", err)
	}
	return s.hydrate(ctx, rows)
}

// ExpiredCheckouts returns up to limit checkouts with an AWAITING_PAYMENT
// order past its payment deadline at now, keyed by (deadline, checkout_id) and
// strictly after `after`. No rows are locked: the sweeper asks payment-svc
// about the checkout before deciding to act on it. A malformed cursor gives
// ErrInvalidCursor, like the order lists.
func (s *Store) ExpiredCheckouts(ctx context.Context, now time.Time, after *app.CheckoutCursor, limit int) ([]app.CheckoutCursor, error) {
	var afterDeadline *time.Time
	var afterCheckoutID *uuid.UUID
	if after != nil {
		cid, ok := parseUUID(after.CheckoutID)
		if !ok {
			return nil, app.ErrInvalidCursor
		}
		d := after.Deadline
		afterDeadline, afterCheckoutID = &d, &cid
	}
	rows, err := s.q.ExpiredCheckouts(ctx, sqlcdb.ExpiredCheckoutsParams{
		Now: &now, AfterDeadline: afterDeadline, AfterCheckoutID: afterCheckoutID, BatchSize: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("expired checkouts: %w", err)
	}
	out := make([]app.CheckoutCursor, 0, len(rows))
	for _, r := range rows {
		out = append(out, app.CheckoutCursor{Deadline: r.Deadline, CheckoutID: r.CheckoutID.String()})
	}
	return out, nil
}

// hydrate converts rows and attaches their items and history (two queries).
func (s *Store) hydrate(ctx context.Context, rows []sqlcdb.Order) ([]domain.Order, error) {
	orders := make([]domain.Order, 0, len(rows))
	if len(rows) == 0 {
		return orders, nil
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	items, err := s.q.ListItemsByOrderIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list order items: %w", err)
	}
	history, err := s.q.ListHistoryByOrderIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list order history: %w", err)
	}
	itemsBy := map[uuid.UUID][]domain.Item{}
	for _, it := range items {
		itemsBy[it.OrderID] = append(itemsBy[it.OrderID], toDomainItem(it))
	}
	historyBy := map[uuid.UUID][]domain.HistoryEntry{}
	for _, h := range history {
		historyBy[h.OrderID] = append(historyBy[h.OrderID], toDomainHistory(h))
	}
	for _, r := range rows {
		o, err := toDomainOrder(r)
		if err != nil {
			return nil, err
		}
		o.Items, o.History = itemsBy[r.ID], historyBy[r.ID]
		orders = append(orders, o)
	}
	return orders, nil
}
