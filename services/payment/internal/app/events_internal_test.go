package app

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
)

func TestRefundEventsMatchTheContractSchemas(t *testing.T) {
	const u = "01928c3a-0000-7000-8000-000000000001"
	orderID := u
	for _, r := range []Refund{
		{ID: u, PaymentID: u, CheckoutID: u, Provider: "fake", OrderID: &orderID, Amount: 1000},
		{ID: u, PaymentID: u, CheckoutID: u, Provider: "stripe", Amount: 1000}, // duplicate: null order
	} {
		for file, p := range map[string]event.Payload{
			"payment-refunded.schema.json": paymentRefunded(r),
			"refund-failed.schema.json":    refundFailed(r, "provider rejected"),
		} {
			c := jsonschema.NewCompiler()
			c.AssertFormat()
			sch, err := c.Compile(filepath.Join("..", "..", "..", "..", "api", "events", file))
			require.NoError(t, err)
			b, err := event.Encode(p, time.Now())
			require.NoError(t, err)
			inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
			require.NoError(t, err)
			assert.NoError(t, sch.Validate(inst), string(b))
		}
	}
}
