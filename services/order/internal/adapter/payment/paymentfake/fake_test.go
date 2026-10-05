package paymentfake_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/payment/paymentfake"
)

var ctx = context.Background()

const (
	checkoutA = "01950000-0000-7000-8000-00000000000a"
	checkoutB = "01950000-0000-7000-8000-00000000000b"
)

func setup(t *testing.T) (*paymentfake.Fake, paymentv1.PaymentInternalClient) {
	t.Helper()
	f := paymentfake.New()
	return f, paymentv1.NewPaymentInternalClient(f.Dial(t))
}

func cancel(t *testing.T, c paymentv1.PaymentInternalClient, checkoutID string) (paymentv1.Outcome, error) {
	t.Helper()
	resp, err := c.CancelCheckoutPayments(ctx, &paymentv1.CancelCheckoutPaymentsRequest{CheckoutId: checkoutID})
	return resp.GetOutcome(), err
}

func TestTheDefaultAnswerIsPaid(t *testing.T) {
	_, c := setup(t)

	out, err := cancel(t, c, checkoutA)
	require.NoError(t, err)
	assert.Equal(t, paymentv1.Outcome_OUTCOME_PAID, out, "an unrelated test must never be timed out by the fake")
}

func TestAnOutcomePersistsAcrossCalls(t *testing.T) {
	f, c := setup(t)
	f.SetOutcome(checkoutA, paymentv1.Outcome_OUTCOME_NOT_PAID)

	for range 3 {
		out, err := cancel(t, c, checkoutA)
		require.NoError(t, err)
		assert.Equal(t, paymentv1.Outcome_OUTCOME_NOT_PAID, out)
	}
	assert.Equal(t, 3, f.Calls(checkoutA))
}

func TestCallsAreCountedPerCheckout(t *testing.T) {
	f, c := setup(t)
	f.SetOutcome(checkoutB, paymentv1.Outcome_OUTCOME_NOT_PAID)

	_, err := cancel(t, c, checkoutA)
	require.NoError(t, err)
	_, err = cancel(t, c, checkoutB)
	require.NoError(t, err)
	_, err = cancel(t, c, checkoutB)
	require.NoError(t, err)

	assert.Equal(t, 1, f.Calls(checkoutA))
	assert.Equal(t, 2, f.Calls(checkoutB))
}

func TestSetDefaultReplacesTheAnswerForEveryCheckout(t *testing.T) {
	f, c := setup(t)
	f.SetDefault(paymentv1.Outcome_OUTCOME_NOT_PAID)

	out, err := cancel(t, c, checkoutA)
	require.NoError(t, err)
	assert.Equal(t, paymentv1.Outcome_OUTCOME_NOT_PAID, out)
}

func TestSetOutcomeClearsAnErrorSetForTheCheckout(t *testing.T) {
	f, c := setup(t)
	f.SetError(checkoutA, codes.Unavailable)

	_, err := cancel(t, c, checkoutA)
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))

	f.SetOutcome(checkoutA, paymentv1.Outcome_OUTCOME_NOT_PAID)

	out, err := cancel(t, c, checkoutA)
	require.NoError(t, err)
	assert.Equal(t, paymentv1.Outcome_OUTCOME_NOT_PAID, out)
}

func TestAnInjectedErrorIsSticky(t *testing.T) {
	f, c := setup(t)
	f.SetError(checkoutA, codes.NotFound)

	for range 2 {
		_, err := cancel(t, c, checkoutA)
		require.Error(t, err)
	}
	assert.Equal(t, 2, f.Calls(checkoutA))
}

func TestTheDelayHonoursTheCallersDeadline(t *testing.T) {
	f, c := setup(t)
	f.SetDelay(10 * time.Second)

	dctx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()

	start := time.Now()
	resp, err := c.CancelCheckoutPayments(dctx, &paymentv1.CancelCheckoutPaymentsRequest{CheckoutId: checkoutA})
	assert.Nil(t, resp)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
	assert.Less(t, time.Since(start), 5*time.Second)
}
