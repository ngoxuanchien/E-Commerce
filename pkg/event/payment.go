package event

import "time"

// OrderCompleted is published by order-svc when an order reaches COMPLETED
// (by the buyer or automatically after AUTO_COMPLETE_AFTER).
type OrderCompleted struct {
	OrderID          string `json:"order_id"`
	CheckoutID       string `json:"checkout_id"`
	SellerUserID     string `json:"seller_user_id"`
	PaymentMethod    string `json:"payment_method"`
	Total            int64  `json:"total"`
	CommissionAmount int64  `json:"commission_amount"`
	CompletedBy      string `json:"completed_by"` // CompletedByBuyer | CompletedBySystem
}

func (OrderCompleted) EventType() Type { return TypeOrderCompleted }

// Allocation is one order's share of a captured payment.
type Allocation struct {
	OrderID          string `json:"order_id"`
	SellerUserID     string `json:"seller_user_id"`
	Amount           int64  `json:"amount"`
	CommissionAmount int64  `json:"commission_amount"`
}

// PaymentSucceeded: a provider confirmed a charge. Allocations is empty
// (never null) for a DUPLICATE payment, which is refunded automatically.
type PaymentSucceeded struct {
	PaymentID   string       `json:"payment_id"`
	CheckoutID  string       `json:"checkout_id"`
	BuyerID     string       `json:"buyer_id"`
	Provider    string       `json:"provider"`
	ProviderRef string       `json:"provider_ref"`
	Amount      int64        `json:"amount"`
	Allocations []Allocation `json:"allocations"`
}

func (PaymentSucceeded) EventType() Type { return TypePaymentSucceeded }

type PaymentFailed struct {
	PaymentID  string `json:"payment_id"`
	CheckoutID string `json:"checkout_id"`
	Provider   string `json:"provider"`
	Reason     string `json:"reason"`
}

func (PaymentFailed) EventType() Type { return TypePaymentFailed }

// PaymentRefunded: OrderID is nil for the refund of a duplicate payment.
type PaymentRefunded struct {
	RefundID   string  `json:"refund_id"`
	PaymentID  string  `json:"payment_id"`
	CheckoutID string  `json:"checkout_id"`
	OrderID    *string `json:"order_id"`
	Amount     int64   `json:"amount"`
	Provider   string  `json:"provider"`
}

func (PaymentRefunded) EventType() Type { return TypePaymentRefunded }

type RefundFailed struct {
	RefundID   string  `json:"refund_id"`
	PaymentID  string  `json:"payment_id"`
	CheckoutID string  `json:"checkout_id"`
	OrderID    *string `json:"order_id"`
	Amount     int64   `json:"amount"`
	Provider   string  `json:"provider"`
	Reason     string  `json:"reason"`
}

func (RefundFailed) EventType() Type { return TypeRefundFailed }

type PayoutRequested struct {
	PayoutID     string `json:"payout_id"`
	SellerUserID string `json:"seller_user_id"`
	Amount       int64  `json:"amount"`
}

func (PayoutRequested) EventType() Type { return TypePayoutRequested }

type PayoutSettled struct {
	PayoutID     string    `json:"payout_id"`
	SellerUserID string    `json:"seller_user_id"`
	Amount       int64     `json:"amount"`
	SettledAt    time.Time `json:"settled_at"`
}

func (PayoutSettled) EventType() Type { return TypePayoutSettled }

type PayoutFailed struct {
	PayoutID     string `json:"payout_id"`
	SellerUserID string `json:"seller_user_id"`
	Amount       int64  `json:"amount"`
	Reason       string `json:"reason"`
}

func (PayoutFailed) EventType() Type { return TypePayoutFailed }
