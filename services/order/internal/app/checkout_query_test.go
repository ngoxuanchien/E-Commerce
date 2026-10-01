package app_test

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

func TestCheckoutQuery(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true})
	q := app.NewCheckoutQuery(e.store)
	svc := app.NewOrderService(e.store, e.clock.Now)

	t.Run("an online checkout returns every order and the money fields", func(t *testing.T) {
		orders := awaitingCheckout(t, e, 2, 30*time.Minute)
		first := orders[0]

		// The buyer cancels one order, which cancels the whole checkout, so
		// GetCheckout has to report both orders even though none is awaiting
		// payment any more.
		_, err := svc.Act(ctx, app.ActInput{
			OrderID: first.ID, Actor: domain.Actor{ID: first.BuyerID, Role: domain.RoleBuyer},
			Action: domain.ActionCancel, Note: "changed my mind",
		})
		require.NoError(t, err)

		view, err := q.GetCheckout(ctx, first.CheckoutID)
		require.NoError(t, err)
		assert.Equal(t, first.CheckoutID, view.CheckoutID)
		assert.Equal(t, first.BuyerID, view.BuyerID)
		assert.Equal(t, domain.PaymentVNPay, view.PaymentMethod)
		require.NotNil(t, view.PaymentDeadline)
		assert.True(t, orders[0].PaymentDeadline.Equal(*view.PaymentDeadline),
			"the deadline of the first order that has one")

		require.Len(t, view.Orders, 2, "a cancelled order is still part of the checkout")
		ids := []string{view.Orders[0].ID, view.Orders[1].ID}
		assert.True(t, slices.IsSorted(ids), "orders come back sorted by id: %v", ids)
		for _, o := range view.Orders {
			assert.Equal(t, domain.StatusCancelled, o.Status)
			assert.Equal(t, first.CheckoutID, o.CheckoutID)
			assert.Equal(t, first.BuyerID, o.BuyerID)
			assert.Equal(t, first.Subtotal, o.Subtotal)
			assert.Equal(t, first.Total, o.Total)
			assert.Equal(t, first.CommissionAmount, o.CommissionAmount)
			assert.NotEmpty(t, o.SellerUserID)
		}
	})

	t.Run("a COD checkout has no payment deadline", func(t *testing.T) {
		orders := checkout(t, e, 2, domain.PaymentCOD, 0)
		view, err := q.GetCheckout(ctx, orders[0].CheckoutID)
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentCOD, view.PaymentMethod)
		assert.Nil(t, view.PaymentDeadline)
		assert.Equal(t, orders[0].BuyerID, view.BuyerID)
		require.Len(t, view.Orders, 2)
		for _, o := range view.Orders {
			assert.Equal(t, domain.StatusPendingConfirmation, o.Status)
			assert.Zero(t, o.CommissionAmount, "COD orders carry no commission")
		}
	})

	t.Run("an unknown checkout is not found", func(t *testing.T) {
		_, err := q.GetCheckout(ctx, uid())
		assert.ErrorIs(t, err, app.ErrNotFound)
	})
}
