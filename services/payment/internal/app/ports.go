// Package app holds payment-svc's use cases and the ports they depend on.
package app

import (
	"context"
	"errors"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// Allocation is one order's share of a payment.
type Allocation struct {
	OrderID, SellerUserID    string
	Amount, CommissionAmount int64
}

// NewPayment is a payment together with its per-order allocations.
type NewPayment struct {
	domain.Payment
	Allocations []Allocation
}

// Refund is a pending or settled refund. OrderID is nil for the full refund of a duplicate payment.
// CheckoutID and Provider are derived from the payment when read; they are ignored on write.
type Refund struct {
	ID, PaymentID, CheckoutID, Provider string
	OrderID                             *string
	Amount                              int64
	Status                              string // PENDING | SUCCEEDED | FAILED
	ProviderRefundRef                   string
	Attempts                            int
	NextAttemptAt                       time.Time
	LastError                           string
}

// Store is the read side plus the transaction entry point of payment persistence.
type Store interface {
	InTx(ctx context.Context, fn func(Tx) error) error
	GetPayment(ctx context.Context, id string) (domain.Payment, error)                         // ErrNotFound
	GetPaymentByProviderRef(ctx context.Context, provider, ref string) (domain.Payment, error) // ErrNotFound
	ListCheckoutPayments(ctx context.Context, checkoutID string) ([]domain.Payment, error)
	ListPendingOlderThan(ctx context.Context, before time.Time, limit int) ([]domain.Payment, error)
	GetRefundByProviderRef(ctx context.Context, provider, ref string) (Refund, error) // ErrNotFound
	PurgeProviderEventsBefore(ctx context.Context, t time.Time) (int64, error)
	PurgeIdempotencyKeysBefore(ctx context.Context, t time.Time) (int64, error)
}

// Tx is the write side, inside one database transaction.
type Tx interface {
	InsertPayment(ctx context.Context, p NewPayment) error
	LockCheckoutPayments(ctx context.Context, checkoutID string) ([]domain.Payment, error) // SELECT … FOR UPDATE ORDER BY id
	UpdatePayment(ctx context.Context, p domain.Payment) error                             // version check → ErrConcurrentUpdate; bumps version, updated_at
	SetSession(ctx context.Context, paymentID, providerRef, redirectURL string) error
	Allocations(ctx context.Context, paymentID string) ([]Allocation, error)
	InsertProviderEvent(ctx context.Context, provider, key string, at time.Time) (first bool, err error)
	RecordCancellation(ctx context.Context, orderID, checkoutID string, at time.Time) (first bool, err error)
	IsCancelled(ctx context.Context, orderID string) (bool, error)
	CapturedAllocationFor(ctx context.Context, orderID string) (p domain.Payment, a Allocation, ok bool, err error)
	InsertRefund(ctx context.Context, r Refund) (created bool, err error)            // ON CONFLICT DO NOTHING on both uniques
	ClaimDueRefunds(ctx context.Context, now time.Time, limit int) ([]Refund, error) // PENDING, next_attempt_at <= now, FOR UPDATE SKIP LOCKED
	UpdateRefund(ctx context.Context, r Refund) error
	Publish(ctx context.Context, key string, p event.Payload, at time.Time) error // outbox, topic event.TopicPaymentEvents
	MarkProcessed(ctx context.Context, eventID string) (first bool, err error)    // pkg/inbox, consumer "payment-svc"
}

var (
	ErrNotFound         = errors.New("not found")
	ErrConcurrentUpdate = errors.New("payment modified concurrently")
)
