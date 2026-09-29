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
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

const (
	orderID = "01928c3a-0000-7000-8000-000000000001"
	buyerID = "01928c3a-0000-7000-8000-000000000002"
)

func TestTransitionEventsForSystemChangeOmitActorID(t *testing.T) {
	o := domain.Order{ID: orderID}
	evs := transitionEvents(o, domain.StatusChange{From: domain.StatusPending, To: domain.StatusPendingConfirmation, Actor: domain.SystemActor()})
	assert.Equal(t, []event.Payload{event.OrderStatusChanged{OrderID: orderID, From: "PENDING", To: "PENDING_CONFIRMATION", ActorRole: "system"}}, evs)
}

func TestTransitionEventsForCancellationAddOrderCancelled(t *testing.T) {
	reason := "đổi ý"
	o := domain.Order{ID: orderID, CancelReason: &reason}
	buyer := domain.Actor{ID: buyerID, Role: domain.RoleBuyer}
	evs := transitionEvents(o, domain.StatusChange{From: domain.StatusPendingConfirmation, To: domain.StatusCancelled, Actor: buyer})
	assert.Equal(t, []event.Payload{
		event.OrderStatusChanged{OrderID: orderID, From: "PENDING_CONFIRMATION", To: "CANCELLED", ActorID: buyerID, ActorRole: "buyer"},
		event.OrderCancelled{OrderID: orderID, Reason: reason, CancelledBy: "buyer"},
	}, evs)
}

func TestEventsMatchTheContractSchemas(t *testing.T) {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	dir := filepath.Join("..", "..", "..", "..", "api", "events")
	schemas := map[event.Type]*jsonschema.Schema{}
	for typ, file := range map[event.Type]string{
		event.TypeOrderCreated:       "order-created.schema.json",
		event.TypeOrderStatusChanged: "order-status-changed.schema.json",
		event.TypeOrderCancelled:     "order-cancelled.schema.json",
	} {
		sch, err := c.Compile(filepath.Join(dir, file))
		require.NoError(t, err)
		schemas[typ] = sch
	}

	reason := domain.CancelCheckoutTimeout
	o := domain.Order{
		ID: orderID, CheckoutID: orderID, BuyerID: buyerID, ShopID: buyerID, SellerUserID: buyerID, Total: 130_000,
		Items: []domain.Item{{SkuID: orderID, Quantity: 1, UnitPrice: 100_000}}, CancelReason: &reason,
	}
	payloads := append([]event.Payload{orderCreated(o)},
		transitionEvents(o, domain.StatusChange{From: domain.StatusPending, To: domain.StatusCancelled, Actor: domain.SystemActor()})...)
	payloads = append(payloads, transitionEvents(o, domain.StatusChange{From: domain.StatusConfirmed, To: domain.StatusCancelled, Actor: domain.Actor{ID: buyerID, Role: domain.RoleSeller}})...)
	for _, p := range payloads {
		b, err := event.Encode(p, time.Now())
		require.NoError(t, err)
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		require.NoError(t, err)
		assert.NoError(t, schemas[p.EventType()].Validate(inst), string(b))
	}
}
