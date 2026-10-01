// Package payment adapts payment-svc's PaymentInternal gRPC API to
// app.Payments: one attempt under a fixed timeout and no retry, since the
// sweeper calls it before locking any row and retries on its next tick.
package payment

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
)

// DefaultTimeout is WS3' §6: one attempt of CancelCheckoutPayments gets 3 s.
const DefaultTimeout = 3 * time.Second

// Client implements app.Payments.
type Client struct {
	rpc     paymentv1.PaymentInternalClient
	timeout time.Duration
}

var _ app.Payments = (*Client)(nil)

// New wraps a connection to payment-svc. A timeout of 0 means DefaultTimeout.
func New(cc grpc.ClientConnInterface, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{rpc: paymentv1.NewPaymentInternalClient(cc), timeout: timeout}
}

// CancelCheckoutPayments cancels the checkout's attempts. Every failure — an
// RPC error or an answer that is not PAID/NOT_PAID — is reported as
// ErrPaymentUnavailable, so the caller leaves the checkout alone and retries.
func (c *Client) CancelCheckoutPayments(ctx context.Context, checkoutID string) (app.PaymentOutcome, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.rpc.CancelCheckoutPayments(ctx, &paymentv1.CancelCheckoutPaymentsRequest{CheckoutId: checkoutID})
	if err != nil {
		return 0, fmt.Errorf("%w: %w", app.ErrPaymentUnavailable, err)
	}
	switch o := resp.GetOutcome(); o {
	case paymentv1.Outcome_OUTCOME_NOT_PAID:
		return app.OutcomeNotPaid, nil
	case paymentv1.Outcome_OUTCOME_PAID:
		return app.OutcomePaid, nil
	default:
		return 0, fmt.Errorf("%w: unexpected outcome %v", app.ErrPaymentUnavailable, o)
	}
}
