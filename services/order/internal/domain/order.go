// Package domain holds order-svc's business rules: the order state machine,
// splitting a checkout into one order per shop, and the cart view. It does no
// I/O and depends only on the standard library.
package domain

import "time"

// Payment methods.
const (
	PaymentCOD    = "COD"
	PaymentVNPay  = "VNPAY"
	PaymentStripe = "STRIPE"
)

// IsOnline reports whether method is paid through payment-svc.
func IsOnline(method string) bool { return method == PaymentVNPay || method == PaymentStripe }

// Cancel reasons set by the system (checkout compensation and the sweeper).
const (
	CancelOutOfStock      = "OUT_OF_STOCK"
	CancelReserveFailed   = "RESERVE_FAILED"
	CancelCheckoutTimeout = "CHECKOUT_TIMEOUT"
	CancelPaymentTimeout  = "PAYMENT_TIMEOUT"
)

// Address is the shipping address snapshot stored with an order.
type Address struct {
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
	Line1    string `json:"line1"`
	Ward     string `json:"ward"`
	District string `json:"district"`
	Province string `json:"province"`
}

// Item is an order line; product data is a snapshot taken at checkout.
type Item struct {
	SkuID       string
	ProductID   string
	ProductName string
	VariantName string
	ImageURL    *string
	UnitPrice   int64
	Quantity    int32
	LineTotal   int64
}

// HistoryEntry records one status change. From is nil for the initial PENDING
// entry; ActorID is empty for the system.
type HistoryEntry struct {
	From      *Status
	To        Status
	ActorID   string
	ActorRole Role
	Note      *string
	At        time.Time
}

// Order is one shop's part of a checkout.
type Order struct {
	ID              string
	CheckoutID      string
	BuyerID         string
	ShopID          string
	ShopName        string
	ShopSlug        string // shop slug snapshot taken at checkout
	SellerUserID    string
	Status          Status
	Items           []Item
	Subtotal        int64
	ShippingFee     int64
	Total           int64
	ShippingAddress Address
	PaymentMethod   string
	// PaymentStatus is the money state; NONE for COD orders.
	PaymentStatus PaymentStatus
	// PaymentDeadline is when an AWAITING_PAYMENT order is cancelled if still
	// unpaid; nil otherwise.
	PaymentDeadline *time.Time
	// CommissionRateBps is the platform commission rate (basis points) snapshot
	// taken at checkout.
	CommissionRateBps int32
	// CommissionAmount is the commission in minor units, computed at checkout.
	CommissionAmount int64
	// DeliveredAt is when the order reached DELIVERED; nil before that.
	DeliveredAt  *time.Time
	CancelReason *string
	History      []HistoryEntry
	Version      int // optimistic-lock version as stored
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Apply records ch on o: the new status, a history entry and, for
// cancellations, the cancel reason (note). note may be empty. Version is left
// to the persistence layer.
func (o *Order) Apply(ch StatusChange, note string, at time.Time) {
	from := ch.From
	var n *string
	if note != "" {
		n = &note
	}
	o.Status = ch.To
	o.UpdatedAt = at
	if ch.To == StatusCancelled {
		o.CancelReason = n
	}
	switch {
	case ch.From == StatusAwaitingPayment && ch.To == StatusPendingConfirmation:
		o.PaymentStatus = PaymentPaid
	case ch.To == StatusCancelled && o.PaymentStatus == PaymentPaid:
		o.PaymentStatus = PaymentRefundPending
	case ch.To == StatusDelivered:
		d := at
		o.DeliveredAt = &d
	}
	o.History = append(o.History, HistoryEntry{
		From: &from, To: ch.To, ActorID: ch.Actor.ID, ActorRole: ch.Actor.Role, Note: n, At: at,
	})
}

// LateCapture records a payment captured after the order was cancelled
// unpaid: CANCELLED+UNPAID → REFUND_PENDING. It reports whether it applied;
// otherwise o is untouched. Status and History never change.
func (o *Order) LateCapture(at time.Time) bool {
	if o.Status != StatusCancelled || o.PaymentStatus != PaymentUnpaid {
		return false
	}
	o.PaymentStatus = PaymentRefundPending
	o.UpdatedAt = at
	return true
}

// MarkRefunded records a completed refund: REFUND_PENDING or PAID (an
// out-of-order refund) → REFUNDED. It reports whether it applied; otherwise o
// is untouched. Status and History never change.
func (o *Order) MarkRefunded(at time.Time) bool {
	if o.PaymentStatus != PaymentRefundPending && o.PaymentStatus != PaymentPaid {
		return false
	}
	o.PaymentStatus = PaymentRefunded
	o.UpdatedAt = at
	return true
}
