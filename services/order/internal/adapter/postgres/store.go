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
