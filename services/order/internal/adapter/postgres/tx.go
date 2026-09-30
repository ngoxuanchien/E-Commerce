package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/outbox"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// txStore implements app.Tx inside one database transaction.
type txStore struct {
	tx pgx.Tx
	q  *sqlcdb.Queries
}

var _ app.Tx = (*txStore)(nil)

func (t *txStore) InsertOrder(ctx context.Context, o domain.Order) error {
	orderID, err := mustParseUUID(o.ID)
	if err != nil {
		return err
	}
	checkoutID, err := mustParseUUID(o.CheckoutID)
	if err != nil {
		return err
	}
	buyerID, err := mustParseUUID(o.BuyerID)
	if err != nil {
		return err
	}
	shopID, err := mustParseUUID(o.ShopID)
	if err != nil {
		return err
	}
	sellerID, err := mustParseUUID(o.SellerUserID)
	if err != nil {
		return err
	}
	addr, err := json.Marshal(o.ShippingAddress)
	if err != nil {
		return fmt.Errorf("encode shipping address: %w", err)
	}
	if err := t.q.InsertOrder(ctx, sqlcdb.InsertOrderParams{
		ID: orderID, CheckoutID: checkoutID, BuyerID: buyerID, ShopID: shopID, ShopName: o.ShopName,
		SellerUserID: sellerID, Status: string(o.Status), Subtotal: o.Subtotal, ShippingFee: o.ShippingFee,
		Total: o.Total, ShippingAddress: addr, PaymentMethod: o.PaymentMethod, CancelReason: o.CancelReason,
		Version: int32(o.Version), CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
		ShopSlug: o.ShopSlug, PaymentStatus: string(o.PaymentStatus), PaymentDeadline: o.PaymentDeadline,
		CommissionRateBps: o.CommissionRateBps, CommissionAmount: o.CommissionAmount, DeliveredAt: o.DeliveredAt,
	}); err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	for _, it := range o.Items {
		skuID, err := mustParseUUID(it.SkuID)
		if err != nil {
			return err
		}
		productID, err := mustParseUUID(it.ProductID)
		if err != nil {
			return err
		}
		if err := t.q.InsertOrderItem(ctx, sqlcdb.InsertOrderItemParams{
			ID: newUUID(), OrderID: orderID, SkuID: skuID, ProductID: productID, ProductName: it.ProductName,
			VariantName: it.VariantName, ImageUrl: it.ImageURL, UnitPrice: it.UnitPrice,
			Quantity: it.Quantity, LineTotal: it.LineTotal,
		}); err != nil {
			return fmt.Errorf("insert order item: %w", err)
		}
	}
	for _, h := range o.History {
		p, err := historyParams(orderID, h)
		if err != nil {
			return err
		}
		if err := t.q.InsertHistory(ctx, p); err != nil {
			return fmt.Errorf("insert order history: %w", err)
		}
	}
	return nil
}

func (t *txStore) SaveTransition(ctx context.Context, o domain.Order, prevVersion int) error {
	if len(o.History) == 0 {
		return errors.New("save transition: order has no history entry")
	}
	if err := t.update(ctx, o, prevVersion); err != nil {
		return err
	}
	id, err := mustParseUUID(o.ID)
	if err != nil {
		return err
	}
	p, err := historyParams(id, o.History[len(o.History)-1])
	if err != nil {
		return err
	}
	if err := t.q.InsertHistory(ctx, p); err != nil {
		return fmt.Errorf("insert order history: %w", err)
	}
	return nil
}

// update writes o's mutable columns if the stored version is still prevVersion.
func (t *txStore) update(ctx context.Context, o domain.Order, prevVersion int) error {
	id, err := mustParseUUID(o.ID)
	if err != nil {
		return err
	}
	n, err := t.q.UpdateOrder(ctx, sqlcdb.UpdateOrderParams{
		Status: string(o.Status), CancelReason: o.CancelReason, PaymentStatus: string(o.PaymentStatus),
		PaymentDeadline: o.PaymentDeadline, DeliveredAt: o.DeliveredAt, UpdatedAt: o.UpdatedAt,
		ID: id, Version: int32(prevVersion),
	})
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	if n == 0 {
		return app.ErrConcurrentUpdate
	}
	return nil
}

func (t *txStore) SavePaymentStatus(ctx context.Context, o domain.Order, prevVersion int) error {
	return t.update(ctx, o, prevVersion)
}

func (t *txStore) Publish(ctx context.Context, orderID string, p event.Payload, at time.Time) error {
	b, err := event.Encode(p, at)
	if err != nil {
		return err
	}
	return outbox.Write(ctx, t.tx, outbox.Message{Topic: event.TopicOrderEvents, Key: orderID, Payload: b})
}

func (t *txStore) ClaimStalePending(ctx context.Context, before time.Time, limit int) ([]domain.Order, error) {
	rows, err := t.q.ClaimStalePending(ctx, sqlcdb.ClaimStalePendingParams{CreatedBefore: before, BatchSize: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("claim stale pending orders: %w", err)
	}
	orders := make([]domain.Order, 0, len(rows))
	for _, r := range rows {
		o, err := toDomainOrder(r)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, nil
}
