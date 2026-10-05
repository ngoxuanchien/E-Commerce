package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/order"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/order/orderfake"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
)

var ctx = context.Background()

func newClient(t *testing.T, cfg order.Config) (*order.Client, *orderfake.Fake) {
	t.Helper()
	f := orderfake.New()
	return order.New(f.Dial(t), cfg), f
}

func fastConfig() order.Config {
	return order.Config{Timeout: time.Second, Retries: 2, Backoff: time.Millisecond}
}

func TestGetCheckout(t *testing.T) {
	c, f := newClient(t, fastConfig())
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	co := orderfake.NewCheckout("buyer", "STRIPE", deadline, 100_000, 250_000)
	f.Put(co)

	got, err := c.GetCheckout(ctx, co.GetCheckoutId())
	require.NoError(t, err)
	assert.Equal(t, co.GetCheckoutId(), got.ID)
	assert.Equal(t, "buyer", got.BuyerID)
	assert.Equal(t, "STRIPE", got.PaymentMethod)
	require.NotNil(t, got.Deadline)
	assert.True(t, got.Deadline.Equal(deadline))
	require.Len(t, got.Orders, 2)
	o := co.GetOrders()[1]
	assert.Equal(t, app.CheckoutOrder{
		OrderID: o.GetOrderId(), SellerUserID: o.GetSellerUserId(), Status: "AWAITING_PAYMENT",
		Subtotal: o.GetSubtotal(), Total: 250_000, CommissionAmount: o.GetCommissionAmount(),
	}, got.Orders[1])
}

func TestGetCheckoutWithoutDeadline(t *testing.T) {
	c, f := newClient(t, fastConfig())
	co := orderfake.NewCheckout("b", "COD", time.Time{}, 50_000)
	f.Put(co)
	got, err := c.GetCheckout(ctx, co.GetCheckoutId())
	require.NoError(t, err)
	assert.Nil(t, got.Deadline)
}

func TestNotFound(t *testing.T) {
	c, f := newClient(t, fastConfig())
	_, err := c.GetCheckout(ctx, "01890000-0000-7000-8000-000000000001")
	assert.ErrorIs(t, err, app.ErrNotFound)
	assert.Equal(t, 1, f.Calls(), "NOT_FOUND is not retried")
}

func TestRetriesTransientFailures(t *testing.T) {
	c, f := newClient(t, fastConfig())
	co := orderfake.NewCheckout("b", "VNPAY", time.Now().Add(time.Hour), 50_000)
	f.Put(co)

	f.FailNext(codes.Unavailable, 2)
	_, err := c.GetCheckout(ctx, co.GetCheckoutId())
	require.NoError(t, err, "two failures are absorbed by two retries")
	assert.Equal(t, 3, f.Calls())
}

func TestGivesUpAfterTwoRetries(t *testing.T) {
	c, f := newClient(t, fastConfig())
	f.FailNext(codes.Unavailable, 5)
	_, err := c.GetCheckout(ctx, "x")
	assert.ErrorIs(t, err, app.ErrOrdersUnavailable)
	assert.Equal(t, 3, f.Calls(), "1 attempt + 2 retries")
}

func TestDeadlineExceededIsRetriedThenUnavailable(t *testing.T) {
	c, f := newClient(t, order.Config{Timeout: 20 * time.Millisecond, Retries: 2, Backoff: time.Millisecond})
	f.SetDelay(time.Second)
	_, err := c.GetCheckout(ctx, "x")
	assert.ErrorIs(t, err, app.ErrOrdersUnavailable)
	assert.Equal(t, 3, f.Calls())
}

func TestOtherFailuresAreUnavailableWithoutRetry(t *testing.T) {
	c, f := newClient(t, fastConfig())
	f.FailNext(codes.Internal, 1)
	_, err := c.GetCheckout(ctx, "x")
	assert.ErrorIs(t, err, app.ErrOrdersUnavailable)
	assert.Equal(t, 1, f.Calls())
}
