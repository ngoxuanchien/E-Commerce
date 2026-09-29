package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// SkuInfos returns order-facing SKU data; unknown ids are omitted.
func (s *Store) SkuInfos(ctx context.Context, ids []uuid.UUID) ([]domain.SkuInfo, error) {
	if len(ids) == 0 {
		return []domain.SkuInfo{}, nil
	}
	rows, err := s.q.GetSkuInfos(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SkuInfo, len(rows))
	for i, r := range rows {
		out[i] = domain.SkuInfo{
			SkuID: r.SkuID, ProductID: r.ProductID, ProductName: r.ProductName, VariantName: r.VariantName,
			ImageKey: r.ImageKey, Price: r.Price, Stock: r.Stock,
			Available: r.SkuActive && r.ProductStatus == string(domain.StatusActive),
			ShopID:    r.ShopID, ShopName: r.ShopName, ShopSlug: r.ShopSlug, SellerUserID: r.OwnerUserID,
		}
	}
	return out, nil
}

// ReserveStock atomically reserves items for orderID. items must be
// normalized (merged and sorted by SKU id, so row locks are taken in a
// global order). A repeated call for the same order is a no-op.
func (s *Store) ReserveStock(ctx context.Context, orderID uuid.UUID, items []domain.StockItem) error {
	return retryable(pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockOrder(ctx, orderID.String()); err != nil {
			return err
		}
		reserved, err := q.OrderHasReservations(ctx, orderID)
		if err != nil {
			return err
		}
		if reserved {
			return nil
		}
		var shortages []domain.StockShortage
		for _, it := range items {
			n, err := q.DecrementStock(ctx, sqlcdb.DecrementStockParams{Qty: it.Quantity, SkuID: it.SkuID})
			if err != nil {
				return err
			}
			if n == 1 {
				continue
			}
			available, err := q.AvailableStock(ctx, it.SkuID)
			if errors.Is(err, pgx.ErrNoRows) {
				available = 0
			} else if err != nil {
				return err
			}
			shortages = append(shortages, domain.StockShortage{SkuID: it.SkuID, Requested: it.Quantity, Available: available})
		}
		if len(shortages) > 0 {
			return &domain.InsufficientStockError{Shortages: shortages} // rolls back every decrement
		}
		for _, it := range items {
			if err := q.InsertReservation(ctx, sqlcdb.InsertReservationParams{OrderID: orderID, SkuID: it.SkuID, Quantity: it.Quantity}); err != nil {
				return err
			}
		}
		return nil
	}))
}

// ReleaseStock returns the reserved stock of orderID; a no-op when nothing is reserved.
func (s *Store) ReleaseStock(ctx context.Context, orderID uuid.UUID) error {
	return retryable(pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		return release(ctx, s.q.WithTx(tx), orderID)
	}))
}

// ReleaseStockForEvent releases orderID once per eventID: the event id is
// recorded in processed_events in the same transaction.
func (s *Store) ReleaseStockForEvent(ctx context.Context, eventID, orderID uuid.UUID) error {
	return retryable(pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		n, err := q.InsertProcessedEvent(ctx, eventID)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil // already processed
		}
		return release(ctx, q, orderID)
	}))
}

func release(ctx context.Context, q *sqlcdb.Queries, orderID uuid.UUID) error {
	if err := q.LockOrder(ctx, orderID.String()); err != nil {
		return err
	}
	rows, err := q.LockReservedForOrder(ctx, orderID) // ordered by sku_id
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := q.IncrementStock(ctx, sqlcdb.IncrementStockParams{Qty: r.Quantity, SkuID: r.SkuID}); err != nil {
			return err
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return q.MarkReleased(ctx, orderID)
}
