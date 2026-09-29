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
		assert.Equal(t, slices.Repeat([]event.Type{event.TypeOrderStatusChanged}, 4), types(evs))
		first, err := event.DecodeData[event.OrderStatusChanged](evs[0])
		require.NoError(t, err)
		assert.Equal(t, event.OrderStatusChanged{OrderID: o.ID, From: "PENDING_CONFIRMATION", To: "CONFIRMED", ActorID: shop.SellerUserID, ActorRole: "seller"}, first)
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
		assert.Equal(t, event.OrderCancelled{OrderID: o.ID, Reason: "đổi ý", CancelledBy: "buyer"}, c)
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
