package app_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/testsupport"
)

var ctx = context.Background()

func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// clock is a controllable app clock (UTC, µs precision like Postgres).
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.t = c.t.Add(d) }

// logBuffer is a goroutine-safe log sink.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *logBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buf.String() }

type harness struct {
	t      *testing.T
	db     *pgxpool.Pool
	store  *postgres.Store
	clock  *clock
	logs   *logBuffer
	logger *slog.Logger
	events *app.EventHandler
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := testsupport.DB(t)
	h := &harness{t: t, db: db, store: postgres.NewStore(db),
		clock: &clock{t: time.Now().UTC().Truncate(time.Microsecond)}, logs: &logBuffer{}}
	h.logger = slog.New(slog.NewJSONHandler(h.logs, nil))
	h.events = app.NewEventHandler(h.store, h.logger, h.clock.Now)
	return h
}

// deliver wraps p in a new envelope (new event id) and handles it successfully.
func (h *harness) deliver(p event.Payload) event.Envelope {
	h.t.Helper()
	env, err := event.New(p, h.clock.Now())
	require.NoError(h.t, err)
	require.NoError(h.t, h.events.Handle(ctx, env))
	return env
}

func (h *harness) handle(env event.Envelope) error { return h.events.Handle(ctx, env) }

func (h *harness) balance(code string) int64 {
	h.t.Helper()
	var b int64
	require.NoError(h.t, h.db.QueryRow(ctx, `SELECT coalesce(sum(e.amount), 0)::bigint
		FROM entries e JOIN accounts a ON a.id = e.account_id WHERE a.code = $1`, code).Scan(&b))
	return b
}

func (h *harness) count(table string) int {
	h.t.Helper()
	var n int
	require.NoError(h.t, h.db.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n))
	return n
}

// total is Σ of every entry; it must always be 0.
func (h *harness) total() int64 {
	h.t.Helper()
	var s int64
	require.NoError(h.t, h.db.QueryRow(ctx, `SELECT coalesce(sum(amount), 0)::bigint FROM entries`).Scan(&s))
	return s
}

func (h *harness) logged(substr string) bool { return strings.Contains(h.logs.String(), substr) }

// orderSpec is one order of a test checkout.
type orderSpec struct {
	amount, commission int64
	seller             uuid.UUID // zero = a fresh seller
}

type checkoutOrder struct {
	id, seller         uuid.UUID
	amount, commission int64
}

type checkout struct {
	id, payment uuid.UUID
	provider    string
	orders      []checkoutOrder
}

func newCheckout(provider string, specs ...orderSpec) checkout {
	c := checkout{id: newID(), payment: newID(), provider: provider}
	for _, s := range specs {
		seller := s.seller
		if seller == uuid.Nil {
			seller = newID()
		}
		c.orders = append(c.orders, checkoutOrder{id: newID(), seller: seller, amount: s.amount, commission: s.commission})
	}
	return c
}

func (c checkout) amount() int64 {
	var s int64
	for _, o := range c.orders {
		s += o.amount
	}
	return s
}

func (c checkout) succeeded() event.PaymentSucceeded {
	p := event.PaymentSucceeded{PaymentID: c.payment.String(), CheckoutID: c.id.String(), BuyerID: newID().String(),
		Provider: c.provider, ProviderRef: "ref-" + c.payment.String(), Amount: c.amount()}
	for _, o := range c.orders {
		p.Allocations = append(p.Allocations, event.Allocation{OrderID: o.id.String(), SellerUserID: o.seller.String(),
			Amount: o.amount, CommissionAmount: o.commission})
	}
	return p
}

func (c checkout) completed(i int) event.OrderCompleted {
	o := c.orders[i]
	return event.OrderCompleted{OrderID: o.id.String(), CheckoutID: c.id.String(), SellerUserID: o.seller.String(),
		PaymentMethod: event.PaymentMethodVNPay, Total: o.amount, CommissionAmount: o.commission, CompletedBy: event.CompletedByBuyer}
}

func (c checkout) refunded(i int) event.PaymentRefunded {
	o := c.orders[i]
	oid := o.id.String()
	return event.PaymentRefunded{RefundID: newID().String(), PaymentID: c.payment.String(), CheckoutID: c.id.String(),
		OrderID: &oid, Amount: o.amount, Provider: c.provider}
}

func escrow(id uuid.UUID) string { return "escrow:" + id.String() }
func wallet(id uuid.UUID) string { return "seller_wallet:" + id.String() }
