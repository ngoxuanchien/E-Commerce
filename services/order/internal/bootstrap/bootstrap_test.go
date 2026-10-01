package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/payment/paymentfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
)

func TestClaimTimeoutOutlastsTheSweepOfADeadRun(t *testing.T) {
	cfg := Config{PendingTimeout: 2 * time.Minute, SweeperInterval: time.Minute}
	assert.Equal(t, 4*time.Minute, cfg.claimTimeout(), "PENDING_TIMEOUT + SWEEPER_INTERVAL + the pre-Tx A margin")
}

// TestNewWiresThePaymentClient pins the wiring: a PaymentConn reaches a working
// app.Payments, which the sweeper's payment-timeout step will use.
func TestNewWiresThePaymentClient(t *testing.T) {
	const checkoutID = "01950000-0000-7000-8000-00000000000a"
	fake := paymentfake.New()
	fake.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)

	// The pool, the cache and the producer are only stored: no check runs and
	// no query is issued here, so the fakes below are never called.
	a := New(Config{PaymentCallTimeout: 200 * time.Millisecond}, Deps{
		Pool: nil, Redis: nil, PaymentConn: fake.Dial(t), Producer: stubProducer{},
	})
	require.NotNil(t, a.pay)

	outcome, err := a.pay.CancelCheckoutPayments(context.Background(), checkoutID)
	require.NoError(t, err)
	assert.Equal(t, app.OutcomeNotPaid, outcome)
	assert.Equal(t, 1, fake.Calls(checkoutID))
}

// stubProducer satisfies Producer; only its method values are taken by New.
type stubProducer struct{}

func (stubProducer) Publish(context.Context, ...kafkax.Message) error { return nil }

func (stubProducer) Ping(context.Context) error { return nil }
