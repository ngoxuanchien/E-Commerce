package app_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
)

type propOrder struct {
	checkout           int
	index              int // position inside its checkout
	id, seller         uuid.UUID
	amount, commission int64
	completed          bool
	refunded           bool
}

type propDuplicate struct {
	checkout  int
	paymentID string
	refunded  bool
}

type propPayout struct {
	seller uuid.UUID
	amount int64
}

// ledgerModel is the expected state of one rapid iteration.
type ledgerModel struct {
	h           *harness
	payouts     *app.PayoutService
	sellers     []uuid.UUID
	checkouts   []checkout
	paid        []bool
	orders      []*propOrder
	duplicates  []*propDuplicate
	envelopes   []event.Envelope // everything delivered, for redelivery
	facts       []event.Payload  // everything delivered, for re-emission under a new event id
	released    map[uuid.UUID]int64
	requested   map[uuid.UUID]int64
	failed      map[uuid.UUID]int64
	outstanding []propPayout // requested, not yet settled or failed
}

func newLedgerModel(t *rapid.T, h *harness) *ledgerModel {
	m := &ledgerModel{
		h:        h,
		payouts:  app.NewPayoutService(h.store, app.PayoutConfig{MinAmount: 1, Now: h.clock.Now, Logger: h.logger}),
		sellers:  []uuid.UUID{newID(), newID()},
		released: map[uuid.UUID]int64{}, requested: map[uuid.UUID]int64{}, failed: map[uuid.UUID]int64{},
	}
	for i := range rapid.IntRange(1, 3).Draw(t, "checkouts") {
		specs := make([]orderSpec, rapid.IntRange(1, 2).Draw(t, "orders"))
		for j := range specs {
			amount := rapid.Int64Range(2, 500000).Draw(t, "amount")
			specs[j] = orderSpec{amount: amount, commission: rapid.Int64Range(0, amount-1).Draw(t, "commission"),
				seller: rapid.SampledFrom(m.sellers).Draw(t, "seller")}
		}
		c := newCheckout(rapid.SampledFrom([]string{"vnpay", "stripe", "fake"}).Draw(t, "provider"), specs...)
		m.checkouts = append(m.checkouts, c)
		m.paid = append(m.paid, false)
		for j, o := range c.orders {
			m.orders = append(m.orders, &propOrder{checkout: i, index: j, id: o.id, seller: o.seller, amount: o.amount, commission: o.commission})
		}
	}
	return m
}

func (m *ledgerModel) handle(t *rapid.T, env event.Envelope) {
	if err := m.h.events.Handle(ctx, env); err != nil {
		t.Fatalf("handle %s %s: %v", env.EventType, env.EventID, err)
	}
}

func (m *ledgerModel) deliver(t *rapid.T, p event.Payload) {
	env, err := event.New(p, m.h.clock.Now())
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	m.handle(t, env)
	m.envelopes = append(m.envelopes, env)
	m.facts = append(m.facts, p)
}

func (m *ledgerModel) available(s uuid.UUID) int64 { return m.released[s] - m.requested[s] + m.failed[s] }

func (m *ledgerModel) pay(t *rapid.T) {
	i := rapid.IntRange(0, len(m.checkouts)-1).Draw(t, "checkout")
	if m.paid[i] {
		t.Skip("already paid")
	}
	m.deliver(t, m.checkouts[i].succeeded())
	m.paid[i] = true
	for _, o := range m.orders {
		if o.checkout == i && o.completed { // a parked completion is released with the capture
			m.released[o.seller] += o.amount - o.commission
		}
	}
}

func (m *ledgerModel) complete(t *rapid.T) {
	o := rapid.SampledFrom(m.orders).Draw(t, "order")
	if o.completed || o.refunded {
		t.Skip("order already final")
	}
	m.deliver(t, m.checkouts[o.checkout].completed(o.index))
	o.completed = true
	if m.paid[o.checkout] {
		m.released[o.seller] += o.amount - o.commission
	}
}

func (m *ledgerModel) refund(t *rapid.T) {
	o := rapid.SampledFrom(m.orders).Draw(t, "order")
	if !m.paid[o.checkout] || o.completed || o.refunded {
		t.Skip("not refundable (ADR-0007: refund only when captured and cancelled)")
	}
	m.deliver(t, m.checkouts[o.checkout].refunded(o.index))
	o.refunded = true
}

func (m *ledgerModel) duplicate(t *rapid.T) {
	i := rapid.IntRange(0, len(m.checkouts)-1).Draw(t, "checkout")
	if !m.paid[i] {
		t.Skip("a duplicate follows the first charge")
	}
	dup := m.checkouts[i].succeeded()
	dup.PaymentID = newID().String()
	dup.Allocations = nil
	m.deliver(t, dup)
	m.duplicates = append(m.duplicates, &propDuplicate{checkout: i, paymentID: dup.PaymentID})
}

func (m *ledgerModel) refundDuplicate(t *rapid.T) {
	if len(m.duplicates) == 0 {
		t.Skip("no duplicate charge")
	}
	d := rapid.SampledFrom(m.duplicates).Draw(t, "duplicate")
	if d.refunded {
		t.Skip("already refunded")
	}
	c := m.checkouts[d.checkout]
	m.deliver(t, event.PaymentRefunded{RefundID: newID().String(), PaymentID: d.paymentID, CheckoutID: c.id.String(),
		Amount: c.amount(), Provider: c.provider})
	d.refunded = true
}

func (m *ledgerModel) redeliver(t *rapid.T) {
	if len(m.envelopes) == 0 {
		t.Skip("nothing delivered yet")
	}
	m.handle(t, rapid.SampledFrom(m.envelopes).Draw(t, "envelope"))
}

func (m *ledgerModel) reemit(t *rapid.T) {
	if len(m.facts) == 0 {
		t.Skip("nothing delivered yet")
	}
	env, err := event.New(rapid.SampledFrom(m.facts).Draw(t, "fact"), m.h.clock.Now())
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	m.handle(t, env)
}

func (m *ledgerModel) payout(t *rapid.T) {
	s := rapid.SampledFrom(m.sellers).Draw(t, "seller")
	amount := rapid.Int64Range(1, 600000).Draw(t, "payout")
	resp, err := m.payouts.Request(ctx, app.PayoutRequestInput{SellerID: s, IdempotencyKey: uuid.NewString(), Amount: amount}, testEncoder)
	if err != nil {
		t.Fatalf("payout request: %v", err)
	}
	if amount <= m.available(s) {
		if resp.Status != 201 {
			t.Fatalf("payout %d with %d available: status %d %s", amount, m.available(s), resp.Status, resp.Body)
		}
		m.requested[s] += amount
		m.outstanding = append(m.outstanding, propPayout{seller: s, amount: amount})
		return
	}
	if resp.Status != 409 {
		t.Fatalf("payout %d with %d available: status %d, want 409", amount, m.available(s), resp.Status)
	}
}

func (m *ledgerModel) settle(t *rapid.T) {
	reject := rapid.Bool().Draw(t, "reject")
	bank := &fakeBank{}
	if reject {
		bank.fail = func(int) error { return &app.TransferRejectedError{Reason: "rejected"} }
	}
	job := app.NewSettleJob(m.h.store, bank, app.SettleConfig{Interval: time.Second, Delay: 0, BatchSize: 10000,
		Now: m.h.clock.Now, Logger: m.h.logger})
	if _, err := job.RunOnce(ctx); err != nil {
		t.Fatalf("settle: %v", err)
	}
	for _, p := range m.outstanding {
		if reject {
			m.failed[p.seller] += p.amount
		}
	}
	m.outstanding = nil
}

func (m *ledgerModel) runPending(t *rapid.T) {
	job := app.NewPendingJob(m.h.store, m.h.events, app.PendingConfig{Interval: time.Second, Now: m.h.clock.Now, Logger: m.h.logger})
	if _, err := job.RunOnce(ctx); err != nil {
		t.Fatalf("pending job: %v", err)
	}
}

func (m *ledgerModel) balance(t *rapid.T, code string) int64 {
	var b int64
	if err := m.h.db.QueryRow(ctx, `SELECT coalesce(sum(e.amount), 0)::bigint
		FROM entries e JOIN accounts a ON a.id = e.account_id WHERE a.code = $1`, code).Scan(&b); err != nil {
		t.Fatalf("balance %s: %v", code, err)
	}
	return b
}

// check runs after every action (rapid's "" action).
func (m *ledgerModel) check(t *rapid.T) {
	tb, err := m.h.store.TrialBalance(ctx)
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !tb.OK() {
		t.Fatalf("trial balance failed: %+v", tb)
	}
	for _, o := range m.orders {
		want := int64(0)
		if m.paid[o.checkout] && !o.completed && !o.refunded {
			want = -o.amount
		}
		if got := m.balance(t, escrow(o.id)); got != want {
			t.Fatalf("escrow of order %s = %d, want %d (paid=%v completed=%v refunded=%v)",
				o.id, got, want, m.paid[o.checkout], o.completed, o.refunded)
		}
	}
	for i, c := range m.checkouts {
		var want int64
		for _, d := range m.duplicates {
			if d.checkout == i && !d.refunded {
				want -= c.amount()
			}
		}
		if got := m.balance(t, "checkout_suspense:"+c.id.String()); got != want {
			t.Fatalf("suspense of checkout %s = %d, want %d", c.id, got, want)
		}
	}
	for _, s := range m.sellers {
		if got := -m.balance(t, wallet(s)); got != m.available(s) {
			t.Fatalf("available(%s) = %d, want releases %d − requested %d + failed %d",
				s, got, m.released[s], m.requested[s], m.failed[s])
		}
	}
}

func TestLedgerInvariantsHoldUnderRandomInterleavings(t *testing.T) {
	h := newHarness(t)
	rapid.Check(t, func(rt *rapid.T) {
		m := newLedgerModel(rt, h)
		rt.Repeat(map[string]func(*rapid.T){
			"pay":              m.pay,
			"complete":         m.complete,
			"refund":           m.refund,
			"duplicate charge": m.duplicate,
			"refund duplicate": m.refundDuplicate,
			"redeliver":        m.redeliver,
			"re-emit":          m.reemit,
			"payout":           m.payout,
			"settle":           m.settle,
			"pending job":      m.runPending,
			"":                 m.check,
		})
	})
}
