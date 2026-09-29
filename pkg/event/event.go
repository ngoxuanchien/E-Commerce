// Package event defines the Kafka event contract (api/events/*.schema.json):
// the envelope, the order, payment and payout event payloads, and encode/decode helpers.
package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// TopicOrderEvents is the topic for all order events; the message key is the order ID.
const TopicOrderEvents = "order.events"

// TopicPaymentEvents carries payment-svc events; the key is the checkout ID.
const TopicPaymentEvents = "payment.events"

// TopicPayoutEvents carries ledger-svc payout events; the key is the seller user ID.
const TopicPayoutEvents = "payout.events"

// Version is the only envelope version defined in P1 and P2.
const Version = 1

// Type is the event_type discriminator.
type Type string

const (
	TypeOrderCreated       Type = "OrderCreated"
	TypeOrderStatusChanged Type = "OrderStatusChanged"
	TypeOrderCancelled     Type = "OrderCancelled"
	TypeOrderCompleted     Type = "OrderCompleted"
	TypePaymentSucceeded   Type = "PaymentSucceeded"
	TypePaymentFailed      Type = "PaymentFailed"
	TypePaymentRefunded    Type = "PaymentRefunded"
	TypeRefundFailed       Type = "RefundFailed"
	TypePayoutRequested    Type = "PayoutRequested"
	TypePayoutSettled      Type = "PayoutSettled"
	TypePayoutFailed       Type = "PayoutFailed"
)

// Values of OrderCompleted.CompletedBy.
const (
	CompletedByBuyer  = "buyer"
	CompletedBySystem = "system"
)

// Payment providers (payment.events "provider").
const (
	ProviderVNPay  = "vnpay"
	ProviderStripe = "stripe"
	ProviderFake   = "fake"
)

// Checkout payment methods (order events "payment_method").
const (
	PaymentMethodCOD    = "COD"
	PaymentMethodVNPay  = "VNPAY"
	PaymentMethodStripe = "STRIPE"
)

// Values of OrderCancelled.CancelledBy.
const (
	CancelledByBuyer  = "buyer"
	CancelledBySeller = "seller"
	CancelledBySystem = "system"
)

var (
	ErrInvalidEnvelope    = errors.New("event: invalid envelope")
	ErrUnsupportedVersion = errors.New("event: unsupported version")
	ErrTypeMismatch       = errors.New("event: type mismatch")
)

// Payload is implemented by every event data struct.
type Payload interface {
	EventType() Type
}

// Envelope wraps every event on the wire.
type Envelope struct {
	EventID    string          `json:"event_id"`
	EventType  Type            `json:"event_type"`
	Version    int             `json:"version"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

type OrderItem struct {
	SkuID     string `json:"sku_id"`
	Quantity  int32  `json:"quantity"`
	UnitPrice int64  `json:"unit_price"`
}

type OrderCreated struct {
	OrderID      string      `json:"order_id"`
	CheckoutID   string      `json:"checkout_id"`
	BuyerID      string      `json:"buyer_id"`
	ShopID       string      `json:"shop_id"`
	SellerUserID string      `json:"seller_user_id"`
	Total        int64       `json:"total"`
	Items        []OrderItem `json:"items"`

	PaymentMethod     string `json:"payment_method,omitempty"`      // P2; absent in P1 payloads
	CommissionRateBps int32  `json:"commission_rate_bps,omitempty"` // P2; 0 for COD
	CommissionAmount  int64  `json:"commission_amount,omitempty"`   // P2; 0 for COD
}

func (OrderCreated) EventType() Type { return TypeOrderCreated }

type OrderStatusChanged struct {
	OrderID   string `json:"order_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	ActorID   string `json:"actor_id,omitempty"` // empty when ActorRole is "system"
	ActorRole string `json:"actor_role"`
}

func (OrderStatusChanged) EventType() Type { return TypeOrderStatusChanged }

type OrderCancelled struct {
	OrderID     string `json:"order_id"`
	Reason      string `json:"reason"`
	CancelledBy string `json:"cancelled_by"`

	// Both are absent on events produced before WS3' (P1 order-svc).
	// Consumers treat a missing PaymentMethod as COD and skip the event.
	CheckoutID    string `json:"checkout_id,omitempty"`    // P2
	PaymentMethod string `json:"payment_method,omitempty"` // P2
}

func (OrderCancelled) EventType() Type { return TypeOrderCancelled }

// New wraps p in an envelope with a fresh UUIDv7 event ID.
func New(p Payload, occurredAt time.Time) (Envelope, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Envelope{}, fmt.Errorf("event: new id: %w", err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		return Envelope{}, fmt.Errorf("event: marshal %s: %w", p.EventType(), err)
	}
	return Envelope{
		EventID:    id.String(),
		EventType:  p.EventType(),
		Version:    Version,
		OccurredAt: occurredAt.UTC().Truncate(time.Millisecond),
		Data:       data,
	}, nil
}

// Encode wraps p in a new envelope and marshals it.
func Encode(p Payload, occurredAt time.Time) ([]byte, error) {
	env, err := New(p, occurredAt)
	if err != nil {
		return nil, err
	}
	return json.Marshal(env)
}

// Decode parses and checks an envelope; the payload stays raw until DecodeData.
func Decode(b []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return Envelope{}, fmt.Errorf("event: decode envelope: %w", err)
	}
	if env.Version != Version {
		return Envelope{}, fmt.Errorf("%w: %d", ErrUnsupportedVersion, env.Version)
	}
	if env.EventID == "" || env.EventType == "" || len(env.Data) == 0 || string(env.Data) == "null" || env.OccurredAt.IsZero() {
		return Envelope{}, ErrInvalidEnvelope
	}
	return env, nil
}

// DecodeData unmarshals the payload of env into T, checking the event type.
func DecodeData[T Payload](env Envelope) (T, error) {
	var out T
	if out.EventType() != env.EventType {
		return out, fmt.Errorf("%w: envelope is %s, want %s", ErrTypeMismatch, env.EventType, out.EventType())
	}
	if err := json.Unmarshal(env.Data, &out); err != nil {
		return out, fmt.Errorf("event: decode %s: %w", env.EventType, err)
	}
	return out, nil
}
