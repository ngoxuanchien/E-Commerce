package stripe_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/stripe"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
)

type observerSpy struct {
	provider string
	in       app.RefundHint
}

func (s *observerSpy) ApplyRefundObserved(_ context.Context, provider string, in app.RefundHint) error {
	s.provider, s.in = provider, in
	return nil
}

func TestRefundHintForForwardsToWorker(t *testing.T) {
	spy := &observerSpy{}
	err := stripe.RefundHintFor(spy)(context.Background(), stripe.RefundHintInput{ProviderRefundRef: "re_1", RefundID: "r-1"})
	assert.NoError(t, err)
	assert.Equal(t, "stripe", spy.provider)
	assert.Equal(t, app.RefundHint{ProviderRefundRef: "re_1", RefundID: "r-1"}, spy.in)
}
