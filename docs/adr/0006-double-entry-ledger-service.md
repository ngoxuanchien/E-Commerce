# ADR 0006 — Double-entry ledger as its own service

- Status: Accepted
- Date: 2026-09-29

## Context

P2 adds online payment, escrow, seller wallets, commission and payouts
(`docs/superpowers/specs/2026-09-29-marketplace-p2-overview.md`). Money now
moves through several stages: from the provider, to a checkout, to an order's
escrow, to a seller's wallet or back to the buyer, and out as a payout. Each
stage is triggered by a different service, and several events can arrive
twice or out of order.

The alternatives considered:
- keep balance columns on payments and wallets, and update them in place.
  Balances drift silently and there is no audit trail;
- put the ledger tables inside payment-svc. That couples the provider
  integration with accounting, and payment-svc would need order and payout
  concerns too;
- a separate double-entry ledger service.

## Decision

- **ledger-svc** owns `ledger_db`. No other service stores balances.
  payment-svc records attempts and refunds, and order-svc records order
  state. Both only publish facts as events.
- Its model is **double-entry**. Every journal transaction has at least two
  entries (debit > 0, credit < 0), and they sum to zero. A deferred
  constraint trigger checks this at commit, so the whole ledger always sums
  to zero.
- It is **append-only**. Triggers reject `UPDATE`, `DELETE` and `TRUNCATE`
  on journal transactions and entries. A mistake is fixed with a reversing
  transaction.
- **Balances are derived** by summing entries, and there is no stored
  balance anywhere. A posting that debits a liability account locks that
  account's row, recomputes its balance and refuses to push it into debit.
  This makes "payout ≤ available" atomic and stops an escrow from being both
  released and refunded.
- **Idempotency** comes from a business key per posting (`pay:{payment_id}`,
  `release:{order_id}`, `refund:{refund_id}`, …). The key is `UNIQUE`, so a
  redelivered or re-emitted event posts once. Consumers also keep
  `processed_events` (ADR-0003).
- **Accounts:** provider clearing (asset), checkout suspense, per-order
  escrow, seller wallet and seller payout-pending (liabilities), commission
  revenue, and platform cash (asset).
- The postings for pay, capture, release, refund, duplicate refund and
  payout request/settle/fail are listed in the P2 WS2' spec §3.
- A `trial-balance` command checks the invariants. The e2e suite runs it
  after every scenario.

## Consequences

- Every figure a seller sees can be explained entry by entry. Double
  processing is impossible by construction, not merely by convention.
- The events that feed the ledger travel on different topics, so the ledger
  must tolerate cross-topic reordering. An `OrderCompleted` that arrives
  before its payment is parked as a pending posting.
- Reading balances costs a `SUM` over an index. That is fine at portfolio
  scale. Snapshots can be added later without changing the model.
- The settlement of provider money into `platform_cash` isn't modelled in
  P2, so `platform_cash` goes negative as payouts settle. This is documented,
  and a later phase can add a settlement posting.
- One more service and database to run and reason about.
