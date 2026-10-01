package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/payment/migrations"
)

var (
	ctx  = context.Background()
	base = time.Now().UTC().Truncate(time.Microsecond)
)

func id() string { return uuid.Must(uuid.NewV7()).String() }

func newStore(t *testing.T) (*postgres.Store, *pgxpool.Pool) {
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return postgres.NewStore(pool), pool
}

// newPayment builds a PENDING payment of a checkout with one allocation.
func newPayment(checkoutID string) app.NewPayment {
	orderID, seller := id(), id()
	return app.NewPayment{
		Payment: domain.Payment{
			ID: id(), CheckoutID: checkoutID, BuyerID: id(), Method: "VNPAY", Provider: "fake",
			Status: domain.StatusPending, Amount: 300_000, ExpiresAt: base.Add(15 * time.Minute),
			CreatedAt: base, UpdatedAt: base,
		},
		Allocations: []app.Allocation{{OrderID: orderID, SellerUserID: seller, Amount: 300_000, CommissionAmount: 15_000}},
	}
}

func insert(t *testing.T, s *postgres.Store, ps ...app.NewPayment) {
	t.Helper()
	for _, p := range ps {
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error { return tx.InsertPayment(ctx, p) }))
	}
}

func TestStore(t *testing.T) {
	s, pool := newStore(t)

	t.Run("insert and read back a payment with allocations", func(t *testing.T) {
		p := newPayment(id())
		insert(t, s, p)
		got, err := s.GetPayment(ctx, p.ID)
		require.NoError(t, err)
		assert.Equal(t, p.Payment, got)
		var allocs []app.Allocation
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
			var err error
			allocs, err = tx.Allocations(ctx, p.ID)
			return err
		}))
		assert.Equal(t, p.Allocations, allocs)

		_, err = s.GetPayment(ctx, id())
		assert.ErrorIs(t, err, app.ErrNotFound)
		_, err = s.GetPayment(ctx, "not-a-uuid")
		assert.ErrorIs(t, err, app.ErrNotFound)

		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
			got, err := tx.GetPayment(ctx, p.ID)
			require.NoError(t, err)
			assert.Equal(t, p.Payment, got)
			_, err = tx.GetPayment(ctx, id())
			assert.ErrorIs(t, err, app.ErrNotFound)
			_, err = tx.GetPayment(ctx, "not-a-uuid")
			assert.ErrorIs(t, err, app.ErrNotFound)
			return nil
		}))
	})

	t.Run("at most one PENDING payment per checkout", func(t *testing.T) {
		co := id()
		insert(t, s, newPayment(co))
		err := s.InTx(ctx, func(tx app.Tx) error { return tx.InsertPayment(ctx, newPayment(co)) })
		assert.Error(t, err)
	})

	t.Run("at most one captured payment per checkout", func(t *testing.T) {
		co := id()
		a, b := newPayment(co), newPayment(co)
		a.Status, a.Captured = domain.StatusSucceeded, true
		b.Status, b.Captured = domain.StatusSucceeded, true
		insert(t, s, a)
		err := s.InTx(ctx, func(tx app.Tx) error { return tx.InsertPayment(ctx, b) })
		assert.Error(t, err)
	})

	t.Run("session sets the provider ref and looks the payment up by it", func(t *testing.T) {
		p := newPayment(id())
		insert(t, s, p)
		ref := "ref-" + p.ID
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error { return tx.SetSession(ctx, p.ID, ref, "https://pay.example/x") }))
		got, err := s.GetPaymentByProviderRef(ctx, "fake", ref)
		require.NoError(t, err)
		assert.Equal(t, p.ID, got.ID)
		assert.Equal(t, ref, got.ProviderRef)
		assert.Equal(t, "https://pay.example/x", got.RedirectURL)
		_, err = s.GetPaymentByProviderRef(ctx, "stripe", ref)
		assert.ErrorIs(t, err, app.ErrNotFound)
	})

	t.Run("update bumps the version and rejects a stale one", func(t *testing.T) {
		p := newPayment(id())
		insert(t, s, p)
		upd := p.Payment
		upd.Status, upd.Captured, upd.ProviderTxnID = domain.StatusSucceeded, true, "txn-1"
		upd.UpdatedAt = base.Add(time.Minute)
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error { return tx.UpdatePayment(ctx, upd) }))
		got, err := s.GetPayment(ctx, p.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusSucceeded, got.Status)
		assert.True(t, got.Captured)
		assert.Equal(t, "txn-1", got.ProviderTxnID)
		assert.Equal(t, 1, got.Version)
		assert.Equal(t, base.Add(time.Minute), got.UpdatedAt)

		err = s.InTx(ctx, func(tx app.Tx) error { return tx.UpdatePayment(ctx, upd) }) // still version 0
		assert.ErrorIs(t, err, app.ErrConcurrentUpdate)
	})

	t.Run("list checkout payments", func(t *testing.T) {
		co := id()
		old := newPayment(co)
		old.Status = domain.StatusFailed
		old.CreatedAt = base.Add(-time.Hour)
		fresh := newPayment(co)
		insert(t, s, old, fresh)
		got, err := s.ListCheckoutPayments(ctx, co)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("ClaimPendingForReconcile leases rows and never hands one to two claimers", func(t *testing.T) {
		p := newPayment(id())
		p.CreatedAt = base.Add(-2 * time.Hour) // older than any before in this test
		insert(t, s, p)

		now := base
		claim := func(limit int) []domain.Payment {
			var got []domain.Payment
			require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
				var err error
				got, err = tx.ClaimPendingForReconcile(ctx, now, now.Add(-time.Hour), now.Add(30*time.Minute), limit)
				return err
			}))
			return got
		}
		ids := func(rows []domain.Payment) []string {
			var out []string
			for _, r := range rows {
				out = append(out, r.ID)
			}
			return out
		}

		// A claimed row is not handed out again before its lease ends.
		first := claim(100)
		require.Contains(t, ids(first), p.ID)
		assert.Empty(t, claim(100), "the lease hides the row until it expires")

		// Once the lease has passed, the row comes back.
		now = base.Add(31 * time.Minute)
		assert.Contains(t, ids(claim(100)), p.ID, "an expired lease releases the row")

		// Two concurrent claimers never receive the same id.
		seed := newPayment(id())
		seed.CreatedAt = base.Add(-2 * time.Hour)
		insert(t, s, seed)
		now = base.Add(time.Hour)
		results := make(chan []string, 2)
		for range 2 {
			go func() {
				var got []string
				require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
					rows, err := tx.ClaimPendingForReconcile(ctx, now, now.Add(-time.Hour), now.Add(30*time.Minute), 100)
					got = ids(rows)
					return err
				}))
				results <- got
			}()
		}
		seen := map[string]bool{}
		for range 2 {
			for _, id := range <-results {
				assert.False(t, seen[id], "id %s claimed twice", id)
				seen[id] = true
			}
		}
		assert.True(t, seen[seed.ID], "the new row is claimable")
	})

	t.Run("provider event is recorded once and purged", func(t *testing.T) {
		key := id()
		for _, want := range []bool{true, false} {
			var first bool
			require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
				var err error
				first, err = tx.InsertProviderEvent(ctx, "stripe", key, base.Add(-48*time.Hour))
				return err
			}))
			assert.Equal(t, want, first)
		}
		n, err := s.PurgeProviderEventsBefore(ctx, base.Add(-24*time.Hour))
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, int64(1))
	})

	t.Run("cancellations are recorded once", func(t *testing.T) {
		order, co := id(), id()
		for _, want := range []bool{true, false} {
			var first bool
			require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
				var err error
				first, err = tx.RecordCancellation(ctx, order, co, base)
				return err
			}))
			assert.Equal(t, want, first)
		}
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
			ok, err := tx.IsCancelled(ctx, order)
			if err != nil {
				return err
			}
			assert.True(t, ok)
			ok, err = tx.IsCancelled(ctx, id())
			assert.False(t, ok)
			return err
		}))
	})

	t.Run("captured allocation is found by order only for the capturing payment", func(t *testing.T) {
		co := id()
		p := newPayment(co)
		p.Status, p.Captured = domain.StatusSucceeded, true
		dup := newPayment(co)
		dup.Status = domain.StatusDuplicate
		dup.Allocations = []app.Allocation{{OrderID: p.Allocations[0].OrderID, SellerUserID: p.Allocations[0].SellerUserID, Amount: 1, CommissionAmount: 0}}
		insert(t, s, p, dup)
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
			got, alloc, ok, err := tx.CapturedAllocationFor(ctx, p.Allocations[0].OrderID)
			require.NoError(t, err)
			assert.True(t, ok)
			assert.Equal(t, p.ID, got.ID)
			assert.Equal(t, p.Allocations[0], alloc)
			_, _, ok, err = tx.CapturedAllocationFor(ctx, id())
			require.NoError(t, err)
			assert.False(t, ok)
			return nil
		}))
	})

	t.Run("refund insert is idempotent per order and per duplicate payment", func(t *testing.T) {
		p := newPayment(id())
		p.Status, p.Captured = domain.StatusSucceeded, true
		insert(t, s, p)
		order := p.Allocations[0].OrderID
		mk := func(orderID *string) app.Refund {
			return app.Refund{ID: id(), PaymentID: p.ID, OrderID: orderID, Amount: 100, Status: "PENDING", NextAttemptAt: base}
		}
		for _, orderID := range []*string{&order, nil} {
			for _, want := range []bool{true, false} {
				var created bool
				require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
					var err error
					created, err = tx.InsertRefund(ctx, mk(orderID))
					return err
				}))
				assert.Equal(t, want, created)
			}
		}
	})

	t.Run("refund update and lookup by provider ref", func(t *testing.T) {
		p := newPayment(id())
		p.Status, p.Captured = domain.StatusSucceeded, true
		insert(t, s, p)
		r := app.Refund{ID: id(), PaymentID: p.ID, Amount: 100, Status: "PENDING", NextAttemptAt: base}
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error { _, err := tx.InsertRefund(ctx, r); return err }))
		r.Status, r.ProviderRefundRef, r.Attempts, r.LastError = "SUCCEEDED", "re_"+r.ID, 2, "boom"
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error { return tx.UpdateRefund(ctx, r) }))
		got, err := s.GetRefundByProviderRef(ctx, "fake", r.ProviderRefundRef)
		require.NoError(t, err)
		r.CheckoutID, r.Provider = p.CheckoutID, "fake"
		assert.WithinDuration(t, time.Now(), got.CreatedAt, time.Minute, "created_at is read back")
		r.CreatedAt = got.CreatedAt
		assert.Equal(t, r, got)
		_, err = s.GetRefundByProviderRef(ctx, "fake", "nope")
		assert.ErrorIs(t, err, app.ErrNotFound)
	})

	t.Run("lock blocks a concurrent locker until commit", func(t *testing.T) {
		co := id()
		insert(t, s, newPayment(co))
		locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- s.InTx(ctx, func(tx app.Tx) error {
				got, err := tx.LockCheckoutPayments(ctx, co)
				if err != nil {
					return err
				}
				if len(got) != 1 {
					return fmt.Errorf("first lock got %d rows", len(got))
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

		second := make(chan error, 1)
		go func() {
			second <- s.InTx(ctx, func(tx app.Tx) error {
				_, err := tx.LockCheckoutPayments(ctx, co)
				return err
			})
		}()
		select {
		case err := <-second:
			t.Fatalf("second locker did not block (err=%v)", err)
		case <-time.After(300 * time.Millisecond):
		}
		close(release)
		require.NoError(t, <-done)
		require.NoError(t, <-second)
	})

	t.Run("ClaimDueRefunds skips rows locked by another tx and future ones", func(t *testing.T) {
		p := newPayment(id())
		p.Status, p.Captured = domain.StatusSucceeded, true
		insert(t, s, p)
		// Other subtests leave PENDING refunds due at base, so claim in an earlier window.
		due, now := base.Add(-100*time.Hour), base.Add(-98*time.Hour)
		r1 := app.Refund{ID: id(), PaymentID: p.ID, OrderID: &p.Allocations[0].OrderID, Amount: 10, Status: "PENDING", NextAttemptAt: due}
		r2 := app.Refund{ID: id(), PaymentID: p.ID, Amount: 10, Status: "PENDING", NextAttemptAt: due.Add(time.Second)}
		future := app.Refund{ID: id(), PaymentID: p.ID, OrderID: ptr(id()), Amount: 10, Status: "PENDING", NextAttemptAt: base.Add(-97 * time.Hour)}
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
			for _, r := range []app.Refund{r1, r2, future} {
				if _, err := tx.InsertRefund(ctx, r); err != nil {
					return err
				}
			}
			return nil
		}))

		locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- s.InTx(ctx, func(tx app.Tx) error {
				got, err := tx.ClaimDueRefunds(ctx, now, 1)
				if err != nil {
					return err
				}
				if len(got) != 1 || got[0].ID != r1.ID {
					return fmt.Errorf("first claim got %d rows", len(got))
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
		var claimed []app.Refund
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
			var err error
			claimed, err = tx.ClaimDueRefunds(ctx, now, 10)
			return err
		}))
		close(release)
		require.NoError(t, <-done)
		require.Len(t, claimed, 1)
		assert.Equal(t, r2.ID, claimed[0].ID)
		assert.Equal(t, p.CheckoutID, claimed[0].CheckoutID)
		assert.Equal(t, "fake", claimed[0].Provider)
		assert.Nil(t, claimed[0].OrderID)
	})

	t.Run("publish writes a payment.events outbox row keyed by checkout", func(t *testing.T) {
		co := id()
		require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
			return tx.Publish(ctx, co, event.PaymentFailed{PaymentID: id(), CheckoutID: co, Reason: "declined"}, base)
		}))
		var topic string
		var payload []byte
		require.NoError(t, pool.QueryRow(ctx, `SELECT topic, payload FROM outbox WHERE msg_key = $1`, co).Scan(&topic, &payload))
		assert.Equal(t, "payment.events", topic)
		env, err := event.Decode(payload)
		require.NoError(t, err)
		assert.Equal(t, event.TypePaymentFailed, env.EventType)
	})

	t.Run("mark processed is first once", func(t *testing.T) {
		eid := id()
		for _, want := range []bool{true, false} {
			var first bool
			require.NoError(t, s.InTx(ctx, func(tx app.Tx) error {
				var err error
				first, err = tx.MarkProcessed(ctx, eid)
				return err
			}))
			assert.Equal(t, want, first)
		}
		var consumer string
		require.NoError(t, pool.QueryRow(ctx, `SELECT consumer FROM processed_events WHERE event_id = $1`, eid).Scan(&consumer))
		assert.Equal(t, "payment-svc", consumer)
	})

	t.Run("purge idempotency keys", func(t *testing.T) {
		_, err := pool.Exec(ctx, `INSERT INTO idempotency_keys (scope, user_id, key, request_hash, created_at) VALUES ('s', $1, 'k', '\x00', $2)`,
			id(), base.Add(-48*time.Hour))
		require.NoError(t, err)
		n, err := s.PurgeIdempotencyKeysBefore(ctx, base.Add(-24*time.Hour))
		require.NoError(t, err)
		assert.Equal(t, int64(1), n)
	})
}

func ptr[T any](v T) *T { return &v }
