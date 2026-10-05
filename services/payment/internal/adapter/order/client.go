// Package order adapts order-svc's OrderInternal gRPC API to app.Orders: a timeout per attempt,
// retries on UNAVAILABLE and DEADLINE_EXCEEDED (GetCheckout is a read), and gRPC → app error mapping.
package order

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orderv1 "github.com/ngoxuanchien/e-commerce/gen/order/v1"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
)

// Config tunes calls to order-svc.
type Config struct {
	Timeout time.Duration // per attempt
	Retries int           // extra attempts after the first
	Backoff time.Duration // wait before retry n is Backoff × n
}

// DefaultConfig is 3 s per attempt, up to 2 retries.
func DefaultConfig() Config {
	return Config{Timeout: 3 * time.Second, Retries: 2, Backoff: 100 * time.Millisecond}
}

// Client implements app.Orders.
type Client struct {
	rpc orderv1.OrderInternalClient
	cfg Config
}

var _ app.Orders = (*Client)(nil)

// New wraps a connection to order-svc.
func New(cc grpc.ClientConnInterface, cfg Config) *Client {
	return &Client{rpc: orderv1.NewOrderInternalClient(cc), cfg: cfg}
}

func (c *Client) GetCheckout(ctx context.Context, checkoutID string) (app.Checkout, error) {
	var resp *orderv1.GetCheckoutResponse
	err := c.call(ctx, func(ctx context.Context) (err error) {
		resp, err = c.rpc.GetCheckout(ctx, &orderv1.GetCheckoutRequest{CheckoutId: checkoutID})
		return err
	})
	if err != nil {
		return app.Checkout{}, err
	}
	out := app.Checkout{ID: resp.GetCheckoutId(), BuyerID: resp.GetBuyerId(), PaymentMethod: resp.GetPaymentMethod()}
	if ts := resp.GetPaymentDeadline(); ts != nil {
		d := ts.AsTime().UTC()
		out.Deadline = &d
	}
	for _, o := range resp.GetOrders() {
		out.Orders = append(out.Orders, app.CheckoutOrder{
			OrderID: o.GetOrderId(), SellerUserID: o.GetSellerUserId(), Status: o.GetStatus(),
			Subtotal: o.GetSubtotal(), Total: o.GetTotal(), CommissionAmount: o.GetCommissionAmount(),
		})
	}
	return out, nil
}

// call runs fn with a per-attempt timeout, retrying transient failures.
func (c *Client) call(ctx context.Context, fn func(context.Context) error) error {
	var err error
	for attempt := 0; ; attempt++ {
		actx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
		err = fn(actx)
		cancel()
		if err == nil {
			return nil
		}
		if attempt >= c.cfg.Retries || !retryable(err) || ctx.Err() != nil {
			return mapError(err)
		}
		timer := time.NewTimer(c.cfg.Backoff * time.Duration(attempt+1))
		select {
		case <-ctx.Done():
			timer.Stop()
			return mapError(err)
		case <-timer.C:
		}
	}
}

func retryable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

func mapError(err error) error {
	if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
		return app.ErrNotFound
	}
	return fmt.Errorf("%w: %w", app.ErrOrdersUnavailable, err)
}
