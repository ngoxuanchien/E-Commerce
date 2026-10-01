package app_test

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

func TestOrderService(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true})
	svc := app.NewOrderService(e.store, e.clock.Now)

	t.Run("get is visible to buyer and seller only", func(t *testing.T) {
		buyer, shop := uid(), catalogfake.NewShop("Shop Get")
		o := e.seedOrder(t, buyer, shop, domain.StatusPendingConfirmation)
		for _, who := range []string{buyer, shop.SellerUserID} {
			got, err := svc.Get(ctx, who, o.ID)
			require.NoError(t, err)
			assert.Equal(t, o, got)
		}
		_, err := svc.Get(ctx, uid(), o.ID)
		assert.ErrorIs(t, err, app.ErrNotFound)
		_, err = svc.Get(ctx, buyer, uid())
		assert.ErrorIs(t, err, app.ErrNotFound)
	})

	t.Run("happy path to completed writes history and events", func(t *testing.T) {
		buyer, shop := uid(), catalogfake.NewShop("Shop Life")
		seller := domain.Actor{ID: shop.SellerUserID, Role: domain.RoleSeller}
		o := e.seedOrder(t, buyer, shop, domain.StatusPendingConfirmation)
		for _, step := range []struct {
			action domain.Action
			actor  domain.Actor
			want   domain.Status
		}{
			{domain.ActionConfirm, seller, domain.StatusConfirmed},
			{domain.ActionShip, seller, domain.StatusShipping},
			{domain.ActionDeliver, seller, domain.StatusDelivered},
			{domain.ActionComplete, domain.Actor{ID: buyer, Role: domain.RoleBuyer}, domain.StatusCompleted},
		} {
			e.clock.Advance(time.Second)
			got, err := svc.Act(ctx, app.ActInput{OrderID: o.ID, Actor: step.actor, Action: step.action, Note: "ok"})
			require.NoError(t, err)
			assert.Equal(t, step.want, got.Status)
		}
		final, err := svc.Get(ctx, buyer, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCompleted, final.Status)
		assert.Len(t, final.History, 6)
		assert.Equal(t, "ok", *final.History[5].Note)

		evs := e.events(t, o.ID)
		assert.Equal(t, append(slices.Repeat([]event.Type{event.TypeOrderStatusChanged}, 4), event.TypeOrderCompleted), types(evs))
		first, err := event.DecodeData[event.OrderStatusChanged](evs[0])
		require.NoError(t, err)
		assert.Equal(t, event.OrderStatusChanged{OrderID: o.ID, From: "PENDING_CONFIRMATION", To: "CONFIRMED", ActorID: shop.SellerUserID, ActorRole: "seller"}, first)
		completed, err := event.DecodeData[event.OrderCompleted](evs[4])
		require.NoError(t, err)
		assert.Equal(t, event.OrderCompleted{
			OrderID: o.ID, CheckoutID: o.CheckoutID, SellerUserID: shop.SellerUserID, PaymentMethod: domain.PaymentCOD,
			Total: o.Total, CommissionAmount: 0, CompletedBy: "buyer",
		}, completed)
	})

	t.Run("buyer cancel while pending confirmation emits OrderCancelled", func(t *testing.T) {
		buyer := uid()
		o := e.seedOrder(t, buyer, catalogfake.NewShop("Shop Cancel"), domain.StatusPendingConfirmation)
		got, err := svc.Act(ctx, app.ActInput{OrderID: o.ID, Actor: domain.Actor{ID: buyer, Role: domain.RoleBuyer}, Action: domain.ActionCancel, Note: "đổi ý"})
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
		assert.Equal(t, "đổi ý", *got.CancelReason)

		evs := e.events(t, o.ID)
		require.Equal(t, []event.Type{event.TypeOrderStatusChanged, event.TypeOrderCancelled}, types(evs))
		c, err := event.DecodeData[event.OrderCancelled](evs[1])
		require.NoError(t, err)
		assert.Equal(t, event.OrderCancelled{
			OrderID: o.ID, Reason: "đổi ý", CancelledBy: "buyer",
			CheckoutID: o.CheckoutID, PaymentMethod: domain.PaymentCOD,
		}, c)
	})

	t.Run("buyer cannot cancel a confirmed order but its seller can", func(t *testing.T) {
		buyer, shop := uid(), catalogfake.NewShop("Shop Confirmed")
		o := e.seedOrder(t, buyer, shop, domain.StatusConfirmed)
		_, err := svc.Act(ctx, app.ActInput{OrderID: o.ID, Actor: domain.Actor{ID: buyer, Role: domain.RoleBuyer}, Action: domain.ActionCancel, Note: "x"})
		assert.ErrorIs(t, err, domain.ErrInvalidTransition)
		got, err := svc.Act(ctx, app.ActInput{OrderID: o.ID, Actor: domain.Actor{ID: shop.SellerUserID, Role: domain.RoleSeller}, Action: domain.ActionCancel, Note: "hết hàng"})
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
	})

	t.Run("buyer cancel of AWAITING_PAYMENT cancels the whole checkout", func(t *testing.T) {
		orders := awaitingCheckout(t, e, 3, 30*time.Minute)
		first, second, timedOut := orders[0], orders[1], orders[2]
		require.Len(t, e.fake.ReleasedOrders(), 0)

		// The payment deadline passed for one order only: the sweeper (Task 7)
		// already cancelled it as the system.
		ch, err := domain.Transition(timedOut, domain.ActionCancel, domain.SystemActor())
		require.NoError(t, err)
		prev := timedOut.Version
		timedOut.Apply(ch, domain.CancelPaymentTimeout, e.clock.Now())
		require.NoError(t, e.store.InTx(ctx, func(tx app.Tx) error { return tx.SaveTransition(ctx, timedOut, prev) }))

		got, err := svc.Act(ctx, app.ActInput{
			OrderID: first.ID, Actor: domain.Actor{ID: first.BuyerID, Role: domain.RoleBuyer},
			Action: domain.ActionCancel, Note: "changed my mind",
		})
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status, "the requested order is returned")

		for _, o := range []domain.Order{first, second} {
			stored, err := svc.Get(ctx, o.BuyerID, o.ID)
			require.NoError(t, err)
			require.Equal(t, domain.StatusCancelled, stored.Status)
			require.NotNil(t, stored.CancelReason)
			assert.Equal(t, "changed my mind", *stored.CancelReason)
			assert.Equal(t, domain.PaymentUnpaid, stored.PaymentStatus)
			last := stored.History[len(stored.History)-1]
			assert.Equal(t, domain.RoleBuyer, last.ActorRole)
			assert.Equal(t, o.BuyerID, last.ActorID)

			evs := e.events(t, o.ID)
			assert.Equal(t, []event.Type{event.TypeOrderStatusChanged, event.TypeOrderCancelled}, types(evs))
			c, err := event.DecodeData[event.OrderCancelled](evs[1])
			require.NoError(t, err)
			assert.Equal(t, event.OrderCancelled{
				OrderID: o.ID, Reason: "changed my mind", CancelledBy: "buyer",
				CheckoutID: o.CheckoutID, PaymentMethod: domain.PaymentVNPay,
			}, c)
		}

		untouched, err := svc.Get(ctx, timedOut.BuyerID, timedOut.ID)
		require.NoError(t, err)
		assert.Equal(t, prev+1, untouched.Version, "the already cancelled order is not touched again")
		assert.Equal(t, domain.StatusCancelled, untouched.Status)
		assert.Len(t, untouched.History, 3)
		assert.Empty(t, e.events(t, timedOut.ID), "the helper writes no events")
	})

	t.Run("seller cannot confirm or cancel AWAITING_PAYMENT", func(t *testing.T) {
		o := awaitingCheckout(t, e, 1, 30*time.Minute)[0]
		seller := domain.Actor{ID: o.SellerUserID, Role: domain.RoleSeller}
		for _, action := range []domain.Action{domain.ActionConfirm, domain.ActionCancel} {
			_, err := svc.Act(ctx, app.ActInput{OrderID: o.ID, Actor: seller, Action: action, Note: "x"})
			assert.ErrorIs(t, err, domain.ErrInvalidTransition, string(action))
		}
		stored, err := svc.Get(ctx, o.BuyerID, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusAwaitingPayment, stored.Status)
	})

	t.Run("buyer cancel of PENDING_CONFIRMATION stays per order", func(t *testing.T) {
		orders := checkout(t, e, 2, domain.PaymentCOD, 0)
		got, err := svc.Act(ctx, app.ActInput{
			OrderID: orders[0].ID, Actor: domain.Actor{ID: orders[0].BuyerID, Role: domain.RoleBuyer},
			Action: domain.ActionCancel, Note: "changed my mind",
		})
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
		assert.Equal(t, orders[0].CheckoutID, orders[1].CheckoutID)

		sibling, err := svc.Get(ctx, orders[1].BuyerID, orders[1].ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusPendingConfirmation, sibling.Status)
		assert.Len(t, e.events(t, orders[1].ID), 0, "the sibling is not cancelled")
	})

	t.Run("strangers get not found", func(t *testing.T) {
		buyer, shop := uid(), catalogfake.NewShop("Shop Owner")
		o := e.seedOrder(t, buyer, shop, domain.StatusPendingConfirmation)
		_, err := svc.Act(ctx, app.ActInput{OrderID: o.ID, Actor: domain.Actor{ID: uid(), Role: domain.RoleSeller}, Action: domain.ActionConfirm})
		assert.ErrorIs(t, err, app.ErrNotFound, "another shop's seller")
		_, err = svc.Act(ctx, app.ActInput{OrderID: o.ID, Actor: domain.Actor{ID: shop.SellerUserID, Role: domain.RoleBuyer}, Action: domain.ActionCancel, Note: "x"})
		assert.ErrorIs(t, err, app.ErrNotFound, "the seller is not the buyer")
	})

	t.Run("concurrent confirm and cancel: exactly one wins", func(t *testing.T) {
		for range 20 {
			buyer, shop := uid(), catalogfake.NewShop("Shop Race")
			o := e.seedOrder(t, buyer, shop, domain.StatusPendingConfirmation)
			inputs := []app.ActInput{
				{OrderID: o.ID, Actor: domain.Actor{ID: shop.SellerUserID, Role: domain.RoleSeller}, Action: domain.ActionConfirm},
				{OrderID: o.ID, Actor: domain.Actor{ID: buyer, Role: domain.RoleBuyer}, Action: domain.ActionCancel, Note: "đổi ý"},
			}
			errs := make([]error, len(inputs))
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i, in := range inputs {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, errs[i] = svc.Act(ctx, in)
				}()
			}
			close(start)
			wg.Wait()

			wins := 0
			for _, err := range errs {
				if err == nil {
					wins++
					continue
				}
				assert.True(t, errors.Is(err, domain.ErrInvalidTransition) || errors.Is(err, app.ErrConcurrentUpdate), "unexpected error: %v", err)
			}
			assert.Equal(t, 1, wins)
			got, err := svc.Get(ctx, buyer, o.ID)
			require.NoError(t, err)
			assert.Len(t, got.History, 3)
			assert.Len(t, e.events(t, o.ID), map[domain.Status]int{domain.StatusConfirmed: 1, domain.StatusCancelled: 2}[got.Status])
		}
	})

	t.Run("list pages newest first with cursors", func(t *testing.T) {
		buyer, shop := uid(), catalogfake.NewShop("Shop List")
		var want []string
		for range 5 {
			e.clock.Advance(time.Second)
			want = append(want, e.seedOrder(t, buyer, shop, domain.StatusPendingConfirmation).ID)
		}
		slices.Reverse(want)

		var got []string
		cursor, pages := "", 0
		for {
			page, err := svc.List(ctx, app.ListInput{Party: app.PartyBuyer, UserID: buyer, Limit: 2, Cursor: cursor})
			require.NoError(t, err)
			pages++
			for _, o := range page.Orders {
				got = append(got, o.ID)
			}
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
		assert.Equal(t, want, got)
		assert.Equal(t, 3, pages)

		confirmed := domain.StatusConfirmed
		page, err := svc.List(ctx, app.ListInput{Party: app.PartySeller, UserID: shop.SellerUserID, Status: &confirmed, Limit: 20})
		require.NoError(t, err)
		assert.Empty(t, page.Orders)
		assert.Nil(t, page.NextCursor)

		_, err = svc.List(ctx, app.ListInput{Party: app.PartyBuyer, UserID: buyer, Limit: 2, Cursor: "garbage"})
		assert.ErrorIs(t, err, app.ErrInvalidCursor)
	})
}
