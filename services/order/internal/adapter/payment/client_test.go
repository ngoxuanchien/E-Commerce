package payment_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/payment"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/payment/paymentfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
)

var ctx = context.Background()

const checkoutID = "01950000-0000-7000-8000-00000000000a"

// setup wires a client to a fresh fake, using default when timeout is 0.
func setup(t *testing.T, timeout time.Duration) (*payment.Client, *paymentfake.Fake) {
	t.Helper()
	f := paymentfake.New()
	return payment.New(f.Dial(t), timeout), f
}

func TestDefaultTimeoutMatchesSpec(t *testing.T) {
	assert.Equal(t, 3*time.Second, payment.DefaultTimeout)
}

func TestCancelCheckoutPaymentsReportsNotPaid(t *testing.T) {
	c, f := setup(t, 0)
	f.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)

	out, err := c.CancelCheckoutPayments(ctx, checkoutID)
	require.NoError(t, err)
	assert.Equal(t, app.OutcomeNotPaid, out)
	assert.Equal(t, 1, f.Calls(checkoutID))
}

func TestCancelCheckoutPaymentsReportsPaid(t *testing.T) {
	c, f := setup(t, 0)
	f.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_PAID)

	out, err := c.CancelCheckoutPayments(ctx, checkoutID)
	require.NoError(t, err)
	assert.Equal(t, app.OutcomePaid, out)
}

func TestRPCErrorsMapToUnavailable(t *testing.T) {
	for name, code := range map[string]codes.Code{
		"unavailable": codes.Unavailable,
		"internal":    codes.Internal,
	} {
		t.Run(name, func(t *testing.T) {
			c, f := setup(t, 0)
			f.SetError(checkoutID, code)

			_, err := c.CancelCheckoutPayments(ctx, checkoutID)
			assert.ErrorIs(t, err, app.ErrPaymentUnavailable)
			assert.Equal(t, 1, f.Calls(checkoutID), "no retry within a tick")
		})
	}
}

func TestUnexpectedOutcomeMapsToUnavailable(t *testing.T) {
	c, f := setup(t, 0)
	f.SetDefault(paymentv1.Outcome_OUTCOME_UNSPECIFIED)

	_, err := c.CancelCheckoutPayments(ctx, checkoutID)
	assert.ErrorIs(t, err, app.ErrPaymentUnavailable)
}

func TestPerCallTimeoutGivesUpWithoutRetrying(t *testing.T) {
	c, f := setup(t, 50*time.Millisecond)
	f.SetDelay(300 * time.Millisecond)

	start := time.Now()
	_, err := c.CancelCheckoutPayments(ctx, checkoutID)
	assert.ErrorIs(t, err, app.ErrPaymentUnavailable)
	assert.Less(t, time.Since(start), 250*time.Millisecond)
	assert.Equal(t, 1, f.Calls(checkoutID), "no retry within a tick")
}

func TestZeroTimeoutMeansTheDefaultNotNoTimeout(t *testing.T) {
	c, f := setup(t, 0)
	f.SetDelay(300 * time.Millisecond)

	start := time.Now()
	out, err := c.CancelCheckoutPayments(ctx, checkoutID)
	require.NoError(t, err, "a 0 timeout must not expire the call immediately")
	assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
	assert.Equal(t, app.OutcomePaid, out)
}
