// Package payment adapts payment-svc's PaymentInternal gRPC API to
// app.Payments: one attempt under a fixed timeout and no retry, since the
// sweeper calls it before locking any row and retries on its next tick.
package payment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

// CancelCheckoutPayments cancels the checkout's attempts. A call that ran out
// of time (the per-call deadline, or DEADLINE_EXCEEDED from the server) is
// reported as ErrPaymentTimeout; every other failure — an RPC error or an
// answer that is not PAID/NOT_PAID — as ErrPaymentUnavailable. Either way the
// caller leaves the checkout alone and retries.
func (c *Client) CancelCheckoutPayments(ctx context.Context, checkoutID string) (app.PaymentOutcome, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.rpc.CancelCheckoutPayments(ctx, &paymentv1.CancelCheckoutPaymentsRequest{CheckoutId: checkoutID})
	if err != nil {
		if status.Code(err) == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
			return 0, fmt.Errorf("%w: %w", app.ErrPaymentTimeout, err)
		}
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
