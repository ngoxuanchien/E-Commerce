package postgres

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

func parseUUID(s string) (uuid.UUID, bool) {
	u, err := uuid.Parse(s)
	return u, err == nil
}

func mustParseUUID(s string) (uuid.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid uuid %q: %w", s, err)
	}
	return u, nil
}

func newUUID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// toDomainOrder converts a row; items and history are attached by the caller.
func toDomainOrder(r sqlcdb.Order) (domain.Order, error) {
	var addr domain.Address
	if err := json.Unmarshal(r.ShippingAddress, &addr); err != nil {
		return domain.Order{}, fmt.Errorf("decode shipping address of order %s: %w", r.ID, err)
	}
	o := domain.Order{
		ID: r.ID.String(), CheckoutID: r.CheckoutID.String(), BuyerID: r.BuyerID.String(),
		ShopID: r.ShopID.String(), ShopName: r.ShopName, SellerUserID: r.SellerUserID.String(),
		Status: domain.Status(r.Status), Subtotal: r.Subtotal, ShippingFee: r.ShippingFee, Total: r.Total,
		ShippingAddress: addr, PaymentMethod: r.PaymentMethod, CancelReason: r.CancelReason,
		Version: int(r.Version), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
		ShopSlug: r.ShopSlug, PaymentStatus: domain.PaymentStatus(r.PaymentStatus),
		CommissionRateBps: r.CommissionRateBps, CommissionAmount: r.CommissionAmount,
	}
	o.PaymentDeadline = utcPtr(r.PaymentDeadline)
	o.DeliveredAt = utcPtr(r.DeliveredAt)
	return o, nil
}

// utcPtr returns a copy of t in UTC, or nil if t is nil.
func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func toDomainItem(r sqlcdb.OrderItem) domain.Item {
	return domain.Item{
		SkuID: r.SkuID.String(), ProductID: r.ProductID.String(), ProductName: r.ProductName,
		VariantName: r.VariantName, ImageURL: r.ImageUrl, UnitPrice: r.UnitPrice,
		Quantity: r.Quantity, LineTotal: r.LineTotal,
	}
}

func toDomainHistory(r sqlcdb.OrderStatusHistory) domain.HistoryEntry {
	h := domain.HistoryEntry{To: domain.Status(r.ToStatus), ActorRole: domain.Role(r.ActorRole), Note: r.Note, At: r.CreatedAt.UTC()}
	if r.FromStatus != nil {
		from := domain.Status(*r.FromStatus)
		h.From = &from
	}
	if r.ActorID != nil {
		h.ActorID = r.ActorID.String()
	}
	return h
}

func historyParams(orderID uuid.UUID, h domain.HistoryEntry) (sqlcdb.InsertHistoryParams, error) {
	p := sqlcdb.InsertHistoryParams{
		ID: newUUID(), OrderID: orderID, ToStatus: string(h.To), ActorRole: string(h.ActorRole), Note: h.Note, CreatedAt: h.At,
	}
	if h.From != nil {
		from := string(*h.From)
		p.FromStatus = &from
	}
	if h.ActorID != "" {
		actor, err := mustParseUUID(h.ActorID)
		if err != nil {
			return p, err
		}
		p.ActorID = &actor
	}
	return p, nil
}
