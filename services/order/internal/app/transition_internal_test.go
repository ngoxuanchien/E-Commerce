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
	orderID    = "01928c3a-0000-7000-8000-000000000001"
	buyerID    = "01928c3a-0000-7000-8000-000000000002"
	checkoutID = "01928c3a-0000-7000-8000-000000000003"
	sellerID   = "01928c3a-0000-7000-8000-000000000004"
	shopID     = "01928c3a-0000-7000-8000-000000000005"
	skuID      = "01928c3a-0000-7000-8000-000000000006"
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

func TestTransitionEventsForCancellationCarryCheckoutAndPaymentMethod(t *testing.T) {
	reason := domain.CancelPaymentTimeout
	o := domain.Order{
		ID: orderID, CheckoutID: checkoutID, PaymentMethod: domain.PaymentVNPay,
		PaymentStatus: domain.PaymentUnpaid, CancelReason: &reason,
	}
	evs := transitionEvents(o, domain.StatusChange{From: domain.StatusAwaitingPayment, To: domain.StatusCancelled, Actor: domain.SystemActor()})
	assert.Equal(t, []event.Payload{
		event.OrderStatusChanged{OrderID: orderID, From: "AWAITING_PAYMENT", To: "CANCELLED", ActorRole: "system"},
		event.OrderCancelled{
			OrderID: orderID, Reason: reason, CancelledBy: "system",
			CheckoutID: checkoutID, PaymentMethod: domain.PaymentVNPay,
		},
	}, evs)
}

// TestTransitionEventsWriteOneOrderCompletedForACODOrder walks the COD happy
// path: every step emits OrderStatusChanged and only the completion also emits
// OrderCompleted (WS3' §3: written for every completion, COD included).
func TestTransitionEventsWriteOneOrderCompletedForACODOrder(t *testing.T) {
	o := domain.Order{
		ID: orderID, CheckoutID: checkoutID, BuyerID: buyerID, ShopID: shopID, SellerUserID: sellerID,
		Total: 130_000, PaymentMethod: domain.PaymentCOD, PaymentStatus: domain.PaymentNone,
		Status: domain.StatusPendingConfirmation,
	}
	seller := domain.Actor{ID: sellerID, Role: domain.RoleSeller}
	buyer := domain.Actor{ID: buyerID, Role: domain.RoleBuyer}
	steps := []struct {
		action domain.Action
		actor  domain.Actor
		to     domain.Status
	}{
		{domain.ActionConfirm, seller, domain.StatusConfirmed},
		{domain.ActionShip, seller, domain.StatusShipping},
		{domain.ActionDeliver, seller, domain.StatusDelivered},
		{domain.ActionComplete, buyer, domain.StatusCompleted},
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	var evs []event.Payload
	for _, step := range steps {
		ch, err := domain.Transition(o, step.action, step.actor)
		require.NoError(t, err)
		require.Equal(t, step.to, ch.To)
		o.Apply(ch, "", at)
		stepEvs := transitionEvents(o, ch)
		if step.to == domain.StatusCompleted {
			require.Len(t, stepEvs, 2)
		} else {
			require.Len(t, stepEvs, 1)
		}
		evs = append(evs, stepEvs...)
	}
	assert.Equal(t, []event.Payload{
		event.OrderStatusChanged{OrderID: orderID, From: "PENDING_CONFIRMATION", To: "CONFIRMED", ActorID: sellerID, ActorRole: "seller"},
		event.OrderStatusChanged{OrderID: orderID, From: "CONFIRMED", To: "SHIPPING", ActorID: sellerID, ActorRole: "seller"},
		event.OrderStatusChanged{OrderID: orderID, From: "SHIPPING", To: "DELIVERED", ActorID: sellerID, ActorRole: "seller"},
		event.OrderStatusChanged{OrderID: orderID, From: "DELIVERED", To: "COMPLETED", ActorID: buyerID, ActorRole: "buyer"},
		event.OrderCompleted{
			OrderID: orderID, CheckoutID: checkoutID, SellerUserID: sellerID, PaymentMethod: domain.PaymentCOD,
			Total: 130_000, CommissionAmount: 0, CompletedBy: "buyer",
		},
	}, evs)
}

func TestTransitionEventsForSystemCompletionCarryTheCommission(t *testing.T) {
	o := domain.Order{
		ID: orderID, CheckoutID: checkoutID, SellerUserID: sellerID, Total: 130_000,
		PaymentMethod: domain.PaymentVNPay, PaymentStatus: domain.PaymentPaid,
		CommissionRateBps: 500, CommissionAmount: domain.Commission(100_000, 500),
	}
	evs := transitionEvents(o, domain.StatusChange{From: domain.StatusDelivered, To: domain.StatusCompleted, Actor: domain.SystemActor()})
	assert.Equal(t, []event.Payload{
		event.OrderStatusChanged{OrderID: orderID, From: "DELIVERED", To: "COMPLETED", ActorRole: "system"},
		event.OrderCompleted{
			OrderID: orderID, CheckoutID: checkoutID, SellerUserID: sellerID, PaymentMethod: domain.PaymentVNPay,
			Total: 130_000, CommissionAmount: 5_000, CompletedBy: "system",
		},
	}, evs)
}

func TestOrderCreatedCarriesTheOnlinePaymentAndCommission(t *testing.T) {
	o := domain.Order{
		ID: orderID, CheckoutID: checkoutID, BuyerID: buyerID, ShopID: shopID, SellerUserID: sellerID, Total: 130_000,
		PaymentMethod: domain.PaymentVNPay, CommissionRateBps: 500, CommissionAmount: domain.Commission(100_000, 500),
		Items: []domain.Item{{SkuID: skuID, Quantity: 1, UnitPrice: 100_000}},
	}
	assert.Equal(t, event.OrderCreated{
		OrderID: orderID, CheckoutID: checkoutID, BuyerID: buyerID, ShopID: shopID, SellerUserID: sellerID, Total: 130_000,
		Items:             []event.OrderItem{{SkuID: skuID, Quantity: 1, UnitPrice: 100_000}},
		PaymentMethod:     domain.PaymentVNPay,
		CommissionRateBps: 500, CommissionAmount: 5_000,
	}, orderCreated(o))
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
		event.TypeOrderCompleted:     "order-completed.schema.json",
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
	online := domain.Order{
		ID: orderID, CheckoutID: checkoutID, BuyerID: buyerID, ShopID: shopID, SellerUserID: sellerID, Total: 130_000,
		Items: []domain.Item{{SkuID: skuID, Quantity: 1, UnitPrice: 100_000}}, CancelReason: &reason,
		PaymentMethod: domain.PaymentVNPay, PaymentStatus: domain.PaymentUnpaid,
		CommissionRateBps: 500, CommissionAmount: domain.Commission(100_000, 500), Status: domain.StatusDelivered,
	}
	payloads := append([]event.Payload{orderCreated(o), orderCreated(online)},
		transitionEvents(o, domain.StatusChange{From: domain.StatusPending, To: domain.StatusCancelled, Actor: domain.SystemActor()})...)
	payloads = append(payloads, transitionEvents(o, domain.StatusChange{From: domain.StatusConfirmed, To: domain.StatusCancelled, Actor: domain.Actor{ID: buyerID, Role: domain.RoleSeller}})...)
	payloads = append(payloads, transitionEvents(online, domain.StatusChange{From: domain.StatusAwaitingPayment, To: domain.StatusCancelled, Actor: domain.SystemActor()})...)
	payloads = append(payloads, transitionEvents(online, domain.StatusChange{From: domain.StatusDelivered, To: domain.StatusCompleted, Actor: domain.SystemActor()})...)
	payloads = append(payloads, transitionEvents(online, domain.StatusChange{From: domain.StatusDelivered, To: domain.StatusCompleted, Actor: domain.Actor{ID: buyerID, Role: domain.RoleBuyer}})...)
	for _, p := range payloads {
		b, err := event.Encode(p, time.Now())
		require.NoError(t, err)
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		require.NoError(t, err)
		assert.NoError(t, schemas[p.EventType()].Validate(inst), string(b))
	}
}
