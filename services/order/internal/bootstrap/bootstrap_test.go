package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/payment/paymentfake"
)

func TestClaimTimeoutOutlastsTheSweepOfADeadRun(t *testing.T) {
	cfg := Config{PendingTimeout: 2 * time.Minute, SweeperInterval: time.Minute}
	assert.Equal(t, 4*time.Minute, cfg.claimTimeout(), "PENDING_TIMEOUT + SWEEPER_INTERVAL + the pre-Tx A margin")
}

// TestNewWiresTheSweeperWithThePaymentClient: the sweeper is built with the
// payment client, and building the App neither blocks on payment-svc nor calls
// it (the client dials lazily, so /readyz does not depend on it — WS3' §6).
// The sweeper actually asking payment-svc is covered by the app and itest
// suites, which have a database.
func TestNewWiresTheSweeperWithThePaymentClient(t *testing.T) {
	const checkoutID = "01950000-0000-7000-8000-00000000000a"
	fake := paymentfake.New()

	// The pool, the cache and the producer are only stored: no check runs and
	// no query is issued here, so the fake is never called. KafkaBrokers is
	// empty, so no consumer is built either.
	a, err := New(Config{PaymentCallTimeout: 200 * time.Millisecond, AutoCompleteAfter: 72 * time.Hour}, Deps{
		Pool: nil, Redis: nil, PaymentConn: fake.Dial(t), Producer: stubProducer{},
	})
	require.NoError(t, err)
	require.NotNil(t, a.Sweeper)
	assert.Nil(t, a.Consumer, "no brokers means no payment.events consumer")
	assert.Zero(t, fake.Calls(checkoutID), "New does not call payment-svc")
}

// stubProducer satisfies Producer; only its method values are taken by New.
type stubProducer struct{}

func (stubProducer) Publish(context.Context, ...kafkax.Message) error { return nil }

func (stubProducer) Ping(context.Context) error { return nil }
