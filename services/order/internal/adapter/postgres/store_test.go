package postgres_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/order/migrations"
)

var (
	ctx  = context.Background()
	base = time.Now().UTC().Truncate(time.Microsecond)
)

// never is a staleBefore that reclaims no idempotency key.
var never time.Time

func id() string { return uuid.Must(uuid.NewV7()).String() }

func newStore(t *testing.T) (*postgres.Store, *pgxpool.Pool) {
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return postgres.NewStore(pool), pool
}

// newOrder builds a PENDING order with two items, as checkout would.
func newOrder(buyer, seller string, createdAt time.Time) domain.Order {
	img := "https://img.example.com/a.jpg"
	return domain.Order{
		ID: id(), CheckoutID: id(), BuyerID: buyer, ShopID: id(), ShopName: "Shop A", SellerUserID: seller,
		Status: domain.StatusPending,
		Items: []domain.Item{
			{SkuID: id(), ProductID: id(), ProductName: "Áo thun", VariantName: "M", ImageURL: &img, UnitPrice: 100_000, Quantity: 2, LineTotal: 200_000},
			{SkuID: id(), ProductID: id(), ProductName: "Quần", VariantName: "L", UnitPrice: 50_000, Quantity: 1, LineTotal: 50_000},
		},
		Subtotal: 250_000, ShippingFee: 30_000, Total: 280_000,
		ShippingAddress: domain.Address{FullName: "Nguyễn Văn A", Phone: "0900000000", Line1: "1 Lê Lợi", Ward: "Bến Nghé", District: "Quận 1", Province: "TP.HCM"},
		PaymentMethod:   domain.PaymentCOD, PaymentStatus: domain.PaymentNone,
		History:   []domain.HistoryEntry{{To: domain.StatusPending, ActorRole: domain.RoleSystem, At: createdAt}},
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func insert(t *testing.T, s *postgres.Store, orders ...domain.Order) {
	t.Helper()
	require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
		for _, o := range orders {
			if err := tx.InsertOrder(ctx, o); err != nil {
				return err
			}
		}
		return nil
	}))
}

// advance applies action by actor to o and saves it with the store.
func advance(s *postgres.Store, o *domain.Order, action domain.Action, actor domain.Actor, note string, at time.Time) error {
	ch, err := domain.Transition(*o, action, actor)
	if err != nil {
		return err
	}
	prev := o.Version
	o.Apply(ch, note, at)
	if err := s.InTx(ctx, func(tx app.Tx) error { return tx.SaveTransition(ctx, *o, prev) }); err != nil {
		return err
	}
	o.Version = prev + 1
	return nil
}

func ids(orders []domain.Order) []string {
	out := make([]string, len(orders))
	for i, o := range orders {
		out[i] = o.ID
	}
	return out
}

func TestStore(t *testing.T) {
	s, pool := newStore(t)

	t.Run("insert and get round-trip", func(t *testing.T) {
		o := newOrder(id(), id(), base)
		insert(t, s, o)
		got, err := s.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, o, got)
	})

	t.Run("P2 fields round-trip", func(t *testing.T) {
		o := newOrder(id(), id(), base)
		o.ShopSlug = "shop-a"
		o.PaymentMethod = "VNPAY"
		o.PaymentStatus = domain.PaymentUnpaid
		o.CommissionRateBps = 500
		o.CommissionAmount = 1000
		insert(t, s, o)
		got, err := s.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, o, got)

		deadline := base.Add(30 * time.Minute)
		o.PaymentDeadline = &deadline
		prev := o.Version
		o.Apply(domain.StatusChange{From: domain.StatusPending, To: domain.StatusAwaitingPayment, Actor: domain.SystemActor()}, "", base.Add(time.Second))
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error { return tx.SaveTransition(ctx, o, prev) }))
		o.Version = prev + 1
		got, err = s.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusAwaitingPayment, got.Status)
		require.NotNil(t, got.PaymentDeadline)
		assert.True(t, deadline.Equal(*got.PaymentDeadline), "deadline round-trips at microsecond precision")
		assert.Len(t, got.History, 2)

		o.PaymentStatus = domain.PaymentPaid
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error { return tx.SavePaymentStatus(ctx, o, o.Version) }))
		got, err = s.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentPaid, got.PaymentStatus)
		assert.Equal(t, 2, got.Version)
		assert.Len(t, got.History, 2, "SavePaymentStatus adds no history row")

		err = s.InTx(ctx, func(tx app.Tx) error { return tx.SavePaymentStatus(ctx, o, 0) })
		assert.ErrorIs(t, err, app.ErrConcurrentUpdate)
	})

	t.Run("unknown or malformed id is not found", func(t *testing.T) {
		_, err := s.GetOrder(ctx, id())
		assert.ErrorIs(t, err, app.ErrNotFound)
		_, err = s.GetOrder(ctx, "not-a-uuid")
		assert.ErrorIs(t, err, app.ErrNotFound)
	})

	t.Run("save transition bumps version and appends history", func(t *testing.T) {
		seller := id()
		o := newOrder(id(), seller, base)
		insert(t, s, o)
		require.NoError(t, advance(s, &o, domain.ActionSubmit, domain.SystemActor(), "", base.Add(time.Second)))
		require.NoError(t, advance(s, &o, domain.ActionCancel, domain.Actor{ID: seller, Role: domain.RoleSeller}, "hết hàng", base.Add(2*time.Second)))

		got, err := s.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, o, got)
		assert.Equal(t, 2, got.Version)
		assert.Equal(t, "hết hàng", *got.CancelReason)
		assert.Len(t, got.History, 3)
	})

	t.Run("stale version is a concurrent update and writes nothing", func(t *testing.T) {
		o := newOrder(id(), id(), base)
		insert(t, s, o)
		stale := o
		stale.History = slices.Clone(o.History)
		require.NoError(t, advance(s, &o, domain.ActionSubmit, domain.SystemActor(), "", base.Add(time.Second)))

		ch, err := domain.Transition(stale, domain.ActionCancel, domain.SystemActor())
		require.NoError(t, err)
		stale.Apply(ch, domain.CancelCheckoutTimeout, base.Add(time.Minute))
		err = s.InTx(ctx, func(tx app.Tx) error { return tx.SaveTransition(ctx, stale, 0) })
		require.ErrorIs(t, err, app.ErrConcurrentUpdate)

		got, err := s.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusPendingConfirmation, got.Status)
		assert.Len(t, got.History, 2)
	})

	t.Run("publish writes an order.events outbox row", func(t *testing.T) {
		orderID := id()
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
			return tx.Publish(ctx, orderID, event.OrderCancelled{OrderID: orderID, Reason: "x", CancelledBy: event.CancelledBySystem}, base)
		}))
		var topic string
		var payload []byte
		require.NoError(t, pool.QueryRow(ctx, `SELECT topic, payload FROM outbox WHERE msg_key = $1`, orderID).Scan(&topic, &payload))
		assert.Equal(t, event.TopicOrderEvents, topic)
		env, err := event.Decode(payload)
		require.NoError(t, err)
		assert.Equal(t, event.TypeOrderCancelled, env.EventType)
	})

	t.Run("rolled back tx leaves no order and no outbox row", func(t *testing.T) {
		o := newOrder(id(), id(), base)
		boom := fmt.Errorf("boom")
		err := s.InTx(ctx, func(tx app.Tx) error {
			if err := tx.InsertOrder(ctx, o); err != nil {
				return err
			}
			if err := tx.Publish(ctx, o.ID, event.OrderCancelled{OrderID: o.ID, Reason: "x", CancelledBy: "system"}, base); err != nil {
				return err
			}
			return boom
		})
		require.ErrorIs(t, err, boom)
		_, err = s.GetOrder(ctx, o.ID)
		assert.ErrorIs(t, err, app.ErrNotFound)
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE msg_key = $1`, o.ID).Scan(&n))
		assert.Zero(t, n)
	})

	t.Run("list filters by party and status, newest first, with keyset cursor", func(t *testing.T) {
		buyer, seller := id(), id()
		var orders []domain.Order
		for i := range 5 {
			orders = append(orders, newOrder(buyer, seller, base.Add(time.Duration(i)*time.Second)))
		}
		insert(t, s, orders...)
		insert(t, s, newOrder(id(), id(), base)) // someone else's order

		page1, err := s.ListOrders(ctx, app.ListQuery{Party: app.PartyBuyer, UserID: buyer, Limit: 2})
		require.NoError(t, err)
		assert.Equal(t, []string{orders[4].ID, orders[3].ID}, ids(page1))

		last := page1[1]
		page2, err := s.ListOrders(ctx, app.ListQuery{Party: app.PartyBuyer, UserID: buyer, Limit: 10, After: &app.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}})
		require.NoError(t, err)
		assert.Equal(t, []string{orders[2].ID, orders[1].ID, orders[0].ID}, ids(page2))
		assert.Len(t, page2[0].Items, 2)
		assert.Len(t, page2[0].History, 1)

		bySeller, err := s.ListOrders(ctx, app.ListQuery{Party: app.PartySeller, UserID: seller, Limit: 10})
		require.NoError(t, err)
		assert.Len(t, bySeller, 5)

		o := orders[0]
		require.NoError(t, advance(s, &o, domain.ActionSubmit, domain.SystemActor(), "", base.Add(time.Minute)))
		pc := domain.StatusPendingConfirmation
		filtered, err := s.ListOrders(ctx, app.ListQuery{Party: app.PartySeller, UserID: seller, Status: &pc, Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, []string{o.ID}, ids(filtered))

		none, err := s.ListOrders(ctx, app.ListQuery{Party: app.PartyBuyer, UserID: id(), Limit: 10})
		require.NoError(t, err)
		assert.NotNil(t, none)
		assert.Empty(t, none)
	})

	t.Run("claim stale pending skips fresh, non-pending and locked rows", func(t *testing.T) {
		cutoff := base.Add(-5 * time.Minute)
		old1 := newOrder(id(), id(), base.Add(-10*time.Minute))
		old2 := newOrder(id(), id(), base.Add(-9*time.Minute))
		fresh := newOrder(id(), id(), base)
		submitted := newOrder(id(), id(), base.Add(-8*time.Minute))
		insert(t, s, old1, old2, fresh, submitted)
		require.NoError(t, advance(s, &submitted, domain.ActionSubmit, domain.SystemActor(), "", base))

		locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- s.InTx(ctx, func(tx app.Tx) error {
				got, err := tx.ClaimStalePending(ctx, cutoff, 1)
				if err != nil {
					return err
				}
				if len(got) != 1 || got[0].ID != old1.ID {
					return fmt.Errorf("first claim got %v", ids(got))
				}
				close(locked)
				<-release
				return nil
			})
		}()
		select {
		case <-locked:
		case err := <-done:
			t.Fatalf("locking transaction ended early: %v", err)
		}

		var claimed []domain.Order
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
			var err error
			claimed, err = tx.ClaimStalePending(ctx, cutoff, 10)
			return err
		}))
		close(release)
		require.NoError(t, <-done)

		assert.Equal(t, []string{old2.ID}, ids(claimed))
		assert.Equal(t, domain.StatusPending, claimed[0].Status)
		assert.Zero(t, claimed[0].Version)
	})

	t.Run("idempotency key lifecycle", func(t *testing.T) {
		user, key, hash := id(), "key-1", []byte("hash-a")
		rec, created, err := s.BeginIdempotency(ctx, user, key, hash, base, never)
		require.NoError(t, err)
		assert.True(t, created)
		assert.Nil(t, rec.Response)
		assert.Equal(t, base, rec.ClaimedAt)

		rec, created, err = s.BeginIdempotency(ctx, user, key, []byte("hash-b"), base, never)
		require.NoError(t, err)
		assert.False(t, created)
		assert.Equal(t, hash, rec.RequestHash)
		assert.Nil(t, rec.Response, "still in progress")

		resp := app.Response{Status: 201, Body: []byte(`{"checkout_id":"x","orders":[]}`)}
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error { return tx.CompleteIdempotencyKey(ctx, user, key, base, resp) }))
		rec, created, err = s.BeginIdempotency(ctx, user, key, hash, base, never)
		require.NoError(t, err)
		assert.False(t, created)
		require.NotNil(t, rec.Response)
		assert.Equal(t, 201, rec.Response.Status)
		assert.JSONEq(t, string(resp.Body), string(rec.Response.Body))

		_, created, err = s.BeginIdempotency(ctx, id(), key, hash, base, never)
		require.NoError(t, err)
		assert.True(t, created, "keys are scoped per user")

		require.NoError(t, s.DeleteIdempotencyKey(ctx, user, key, base))
		_, created, err = s.BeginIdempotency(ctx, user, key, hash, base, never)
		require.NoError(t, err)
		assert.True(t, created)
	})

	t.Run("an orphaned in-progress key is reclaimed once stale", func(t *testing.T) {
		user, hash := id(), []byte("hash-a")
		orphanedAt := base.Add(-10 * time.Minute)
		_, created, err := s.BeginIdempotency(ctx, user, "k", hash, orphanedAt, never)
		require.NoError(t, err)
		require.True(t, created)

		_, created, err = s.BeginIdempotency(ctx, user, "k", hash, base, orphanedAt)
		require.NoError(t, err)
		assert.False(t, created, "not older than staleBefore")
		_, created, err = s.BeginIdempotency(ctx, user, "k", []byte("hash-b"), base, base)
		require.NoError(t, err)
		assert.False(t, created, "a different request never takes over the key")

		rec, created, err := s.BeginIdempotency(ctx, user, "k", hash, base, base.Add(-5*time.Minute))
		require.NoError(t, err)
		assert.True(t, created, "same request, stale and without a response")
		assert.Equal(t, base, rec.ClaimedAt)

		_, created, err = s.BeginIdempotency(ctx, user, "k", hash, base.Add(time.Second), base.Add(-5*time.Minute))
		require.NoError(t, err)
		assert.False(t, created, "the reclaimed key is fresh again")
	})

	t.Run("a completed key is never reclaimed", func(t *testing.T) {
		user, hash := id(), []byte("h")
		old := base.Add(-time.Hour)
		_, _, err := s.BeginIdempotency(ctx, user, "k", hash, old, never)
		require.NoError(t, err)
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
			return tx.CompleteIdempotencyKey(ctx, user, "k", old, app.Response{Status: 201, Body: []byte(`{}`)})
		}))
		rec, created, err := s.BeginIdempotency(ctx, user, "k", hash, base, base)
		require.NoError(t, err)
		assert.False(t, created)
		assert.NotNil(t, rec.Response)
	})

	t.Run("only the current claim may complete or release a key", func(t *testing.T) {
		user, hash := id(), []byte("h")
		old := base.Add(-time.Hour)
		_, _, err := s.BeginIdempotency(ctx, user, "k", hash, old, never)
		require.NoError(t, err)
		_, created, err := s.BeginIdempotency(ctx, user, "k", hash, base, base) // reclaimed
		require.NoError(t, err)
		require.True(t, created)

		err = s.InTx(ctx, func(tx app.Tx) error {
			return tx.CompleteIdempotencyKey(ctx, user, "k", old, app.Response{Status: 201, Body: []byte(`{}`)})
		})
		require.ErrorIs(t, err, app.ErrIdempotencyClaimLost, "the orphaned run lost its claim")
		require.NoError(t, s.DeleteIdempotencyKey(ctx, user, "k", old))

		rec, created, err := s.BeginIdempotency(ctx, user, "k", hash, base, never)
		require.NoError(t, err)
		assert.False(t, created, "the stale run's release did not free the new claim")
		assert.Nil(t, rec.Response, "nor did its completion land")
	})

	t.Run("expired idempotency keys are deleted", func(t *testing.T) {
		old, fresh, hash := id(), id(), []byte("h")
		_, _, err := s.BeginIdempotency(ctx, old, "k", hash, base.Add(-25*time.Hour), never)
		require.NoError(t, err)
		_, _, err = s.BeginIdempotency(ctx, fresh, "k", hash, base, never)
		require.NoError(t, err)

		n, err := s.DeleteIdempotencyKeysBefore(ctx, base.Add(-24*time.Hour))
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, int64(1))
		_, created, err := s.BeginIdempotency(ctx, old, "k", hash, base, never)
		require.NoError(t, err)
		assert.True(t, created)
		_, created, err = s.BeginIdempotency(ctx, fresh, "k", hash, base, never)
		require.NoError(t, err)
		assert.False(t, created)
	})
}
