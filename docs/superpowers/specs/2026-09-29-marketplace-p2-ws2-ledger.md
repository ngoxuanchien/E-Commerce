# WS2': ledger-svc

> Depends on WS0' (`api/openapi/ledger.yaml`, `api/events`, `pkg/event`, `pkg/inbox`, `pkg/idemkey`, `pkg/outbox`). Owns `services/ledger/`. During development the tests produce `payment.events` and `order.events` messages with helpers built on `pkg/event`. No other service is needed. ADR-0006 and ADR-0007 record the design.

## 1. Responsibility

ledger-svc is the single source of truth for money positions:
- funds received from providers;
- per-order escrow;
- seller wallets;
- platform commission revenue;
- payouts.

It is a **double-entry, append-only** ledger, and every balance is **derived** by summing entries. It consumes payment and order events, serves the seller wallet API, runs the simulated payout settlement, and publishes `payout.events`.

## 2. Chart of accounts

Accounts are created lazily, the first time they're used (`INSERT … ON CONFLICT (code) DO NOTHING`).

| Code | Type | Owner / ref | Meaning |
|---|---|---|---|
| `psp_clearing:{vnpay\|stripe\|fake}` | asset | — | Money held at the provider for us |
| `checkout_suspense:{checkout_id}` | liability | buyer | Received for a checkout but not yet allocated to orders. Anything left is a duplicate charge waiting for its refund. |
| `escrow:{order_id}` | liability | seller (`owner_user_id`), `order_id` | Held for one order until it completes or is refunded |
| `seller_wallet:{seller_user_id}` | liability | seller | The seller's available balance |
| `seller_payout_pending:{seller_user_id}` | liability | seller | Withdrawal requested, not yet settled |
| `platform_commission_revenue` | revenue | — | Commission earned |
| `platform_cash` | asset | — | Simulated platform bank account that pays out |

**Sign convention:** `entries.amount` is a signed `int64`, with **debit > 0 and credit < 0**. The balance of an account is `Σ amount`. Liability and revenue accounts normally have a credit balance, so the API shows them as `−Σ`. P2 doesn't model the settlement of `psp_clearing` into `platform_cash`, so `platform_cash` goes negative as payouts settle (ADR-0006).

## 3. Postings

A = the payment amount; aᵢ and cᵢ = the order's allocated total and commission; w = the payout amount. Each row is one journal transaction with a unique `idempotency_key`.

| Kind | Trigger | `idempotency_key` | Debit | Credit |
|---|---|---|---|---|
| `PAY` | `PaymentSucceeded` | `pay:{payment_id}` | `psp_clearing:{provider}` A | `checkout_suspense:{checkout}` A |
| `CAPTURE` | same event, same DB tx, only when `allocations ≠ []` | `capture:{payment_id}` | `checkout_suspense:{checkout}` Σaᵢ | `escrow:{orderᵢ}` aᵢ (one line per allocation) |
| `RELEASE` | `OrderCompleted` (online) | `release:{order_id}` | `escrow:{order}` a | `seller_wallet:{seller}` a − c; `platform_commission_revenue` c (the line is omitted when c = 0) |
| `REFUND` | `PaymentRefunded` with `order_id` | `refund:{refund_id}` | `escrow:{order}` a | `psp_clearing:{provider}` a |
| `DUPLICATE_REFUND` | `PaymentRefunded` with `order_id = null` | `refund:{refund_id}` | `checkout_suspense:{checkout}` A | `psp_clearing:{provider}` A |
| `PAYOUT_REQUEST` | `POST /api/seller/payouts` | `payout:{payout_id}:request` | `seller_wallet:{seller}` w | `seller_payout_pending:{seller}` w |
| `PAYOUT_SETTLE` | settle job, success | `payout:{payout_id}:settle` | `seller_payout_pending:{seller}` w | `platform_cash` w |
| `PAYOUT_FAIL` | settle job, failure | `payout:{payout_id}:fail` | `seller_payout_pending:{seller}` w | `seller_wallet:{seller}` w |

Worked example: a checkout pays 330,000 for order X (subtotal 200,000 + shipping 30,000, commission 10,000) and order Y (70,000 + 30,000, commission 3,500). It is paid by VNPay, then X completes and Y is cancelled.

```
PAY      psp_clearing:vnpay +330000 | checkout_suspense:C −330000
CAPTURE  checkout_suspense:C +330000 | escrow:X −230000 | escrow:Y −100000
RELEASE  escrow:X +230000 | seller_wallet:S1 −220000 | platform_commission_revenue −10000
REFUND   escrow:Y +100000 | psp_clearing:vnpay −100000
→ psp_clearing:vnpay = +230000, seller_wallet:S1 = −220000 (shown as 220,000 available),
  commission = −10000, suspense/escrows = 0, Σ all = 0
```

## 4. Invariants (enforced in the database, and again in code)

1. **Every journal transaction balances:** `Σ entries.amount = 0`, with at least two entries. A `DEFERRABLE INITIALLY DEFERRED` constraint trigger on `entries`, and one on `journal_transactions` for the entry count, checks this at commit. So the whole ledger always sums to 0 (the trial balance).
2. **Append-only:** `BEFORE UPDATE OR DELETE` and `BEFORE TRUNCATE` triggers on `journal_transactions` and `entries` raise an exception. Mistakes are corrected only by new reversing transactions, which P2 never needs.
3. **No liability account goes into debit:**
   - the posting engine locks each **debited liability account** (`SELECT … FOR UPDATE` on `accounts`, in account-id order, to avoid deadlocks);
   - it recomputes the balance and rejects a posting that would leave `Σ > 0`;
   - this is what makes "payout ≤ available" atomic, and it prevents an escrow from being both released and refunded.
   - Asset and revenue accounts are never locked, so hot accounts such as `psp_clearing` don't serialize postings.
4. **Idempotency:**
   - `journal_transactions.idempotency_key` is `UNIQUE`, and a conflict returns the existing transaction as a no-op;
   - consumers also call `inbox.MarkProcessed` in the same DB transaction;
   - together these survive both redelivery and the re-emission of the same fact under a new `event_id`.
5. **An escrow balance is only ever 0 or −aᵢ.** It is 0 before capture, −aᵢ while held, and 0 after release or refund. The property test in §9 checks this.

## 5. Data (`ledger_db`)

```sql
accounts(id uuid pk, code text unique not null, type text not null,   -- asset | liability | revenue
         owner_user_id uuid null, order_id uuid null, created_at)
journal_transactions(id uuid pk, idempotency_key text unique not null, kind text not null,
         refs jsonb not null,             -- {checkout_id?, payment_id?, order_id?, refund_id?, payout_id?}
         source_event_id uuid null, occurred_at timestamptz not null, created_at)
entries(id uuid pk, transaction_id uuid fk, account_id uuid fk,
        amount bigint not null check (amount <> 0), created_at)
escrow_allocations(order_id uuid pk, payment_id uuid, checkout_id uuid, seller_user_id uuid,
        amount bigint, commission_amount bigint, provider text, created_at)   -- immutable, written with CAPTURE
payouts(id uuid pk, seller_user_id uuid, amount bigint check (amount > 0),
        status text,                     -- REQUESTED | SETTLED | FAILED
        failure_reason text null, requested_at, settled_at null, version int)
pending_postings(order_id uuid pk, event jsonb, attempts int, created_at, last_attempt_at)
processed_events, idempotency_keys, outbox    -- pkg/inbox, pkg/idemkey, pkg/outbox
```

Indexes:
- `entries(account_id, created_at desc, id desc)`, for balances and history;
- `accounts(owner_user_id) where code like 'escrow:%'`, for `in_escrow`;
- `payouts(seller_user_id, requested_at desc, id desc)` and `payouts(status, requested_at)`.

`payouts.status` is a workflow field and isn't used for money. The money truth is always the entries.

## 6. Consumers (group `ledger-svc`)

Each handler runs in one DB transaction: `inbox.MarkProcessed`, then the postings.
- **`PaymentSucceeded`**:
  - `PAY`;
  - if there are allocations: `CAPTURE`, plus the `escrow_allocations` rows (escrow accounts are created with `owner_user_id = seller_user_id` and `order_id`);
  - any `pending_postings` for those orders are applied in the same transaction.
- **`OrderCompleted`**:
  - `payment_method = COD` → nothing (processed only).
  - otherwise, if the order has an `escrow_allocations` row → `RELEASE`. The amounts come from the allocation, which is what was captured. If `total` or `commission_amount` on the event differ, the difference is logged as an error, and the allocation wins.
  - no allocation yet → insert `pending_postings`. This happens because `OrderCompleted` and `PaymentSucceeded` travel on different topics, so their relative order isn't guaranteed.
- **`PaymentRefunded`** → `REFUND` (with `order_id`) or `DUPLICATE_REFUND` (`order_id` null).
  - A refund for an order that has no allocation returns a retryable error, which the consumer backs off on. It can't happen in practice, because `payment.events` is keyed by `checkout_id`, so the capture precedes the refund.
  - A refund of an escrow that was already released is rejected by invariant 3. The error is logged and alerted. It can't happen through the state machines in ADR-0007.
- **Pending postings job** (`PENDING_POSTING_INTERVAL`, `FOR UPDATE SKIP LOCKED`): retries each row. A row older than 1 h logs an error on every attempt.

## 7. Wallet API and payouts (`ledger.yaml`)

Every route has the Seller role. The seller is `X-User-Id`.
- **`GET /api/seller/wallet`:**
  - `available = −bal(seller_wallet)`;
  - `pending_payout = −bal(seller_payout_pending)`;
  - `in_escrow = −Σ bal(escrow accounts with owner = seller)`;
  - a seller with no accounts yet gets all zeros.
- **`GET /api/seller/wallet/entries`:** the entries of `seller_wallet:{seller}`, joined with their transaction.
  - `kind` is taken from the transaction kind (`RELEASE`, `PAYOUT_REQUEST` or `PAYOUT_FAIL`);
  - `amount = −entry.amount`, so money into the wallet is positive;
  - `order_id` and `payout_id` come from `refs`;
  - the cursor is `(created_at, id)`.
- **`POST /api/seller/payouts`:**
  1. `idemkey` (`scope=payout`).
  2. Reject `amount < PAYOUT_MIN_AMOUNT` with 400.
  3. Tx: lock the wallet account and compute `available`. If `available < amount` → 409 `INSUFFICIENT_FUNDS {available}`, and the key is completed with that response (a deterministic rejection).
  4. Otherwise insert the payout `REQUESTED`, post `PAYOUT_REQUEST`, and write `PayoutRequested` to the outbox.
  5. 201 `Payout`.
- **`GET /api/seller/payouts`:** the seller's payouts, filterable by `status`.
- **Settle job** (`PAYOUT_SETTLE_INTERVAL`): picks `REQUESTED` payouts older than `PAYOUT_SETTLE_DELAY` with `FOR UPDATE SKIP LOCKED`.
  1. It calls `BankPort.Transfer(payout_id, seller, amount)`. The call is idempotent per `payout_id`. The production adapter is a simulator that always succeeds; tests inject failures.
  2. Tx: `SETTLED` + `PAYOUT_SETTLE` + `PayoutSettled`, or `FAILED` + `PAYOUT_FAIL` + `PayoutFailed`.

  A crash between the transfer and the transaction repeats the transfer on the next run, which is safe because the port is idempotent.

## 8. Operations: the `trial-balance` subcommand

`server trial-balance` connects with `DATABASE_URL` and prints JSON:
- the balance per account type;
- the total, which must be 0;
- the count of unbalanced transactions, which must be 0;
- the count of liability accounts in debit, which must be 0.

It exits 1 if any of these checks fail. WS6' runs it through `docker compose exec ledger /server trial-balance` after every e2e scenario.

## 9. Code layout and testing

```
internal/domain             Account codes, Journal (lines, Σ=0 check), posting builders per kind, sign helpers
internal/app                Poster (lock → check → insert), event handlers, PayoutService, SettleJob, PendingJob, TrialBalance
internal/adapter/postgres   sqlc queries, triggers in migrations, inbox/idemkey/outbox
internal/adapter/kafka      consumer for order.events + payment.events
internal/adapter/bank       BankPort simulator
internal/adapter/http       ledger.yaml handlers
```

- **Unit:**
  - each posting builder produces balanced lines, including the zero-commission case;
  - the worked example in §3;
  - sign conversion for the API.
- **Integration** (Postgres and Kafka containers):
  - The triggers reject an unbalanced transaction, a single-entry transaction, `UPDATE` and `DELETE`.
  - The same event delivered twice, or the same fact under a new `event_id`, produces one posting.
  - `OrderCompleted` before `PaymentSucceeded` → a pending posting, then released once the capture arrives.
  - A COD `OrderCompleted` → no postings.
  - Two concurrent payouts whose sum exceeds `available` → exactly one succeeds and the other gets 409. The wallet never goes into debit.
  - Settle success and failure paths, with their events.
  - `trial-balance` exit codes.
- **Property test** (`pgregory.net/rapid`): random interleavings of captures, completions, refunds, duplicates, redeliveries and payouts, over a few checkouts. After each step:
  - Σ = 0;
  - every escrow is 0 or −aᵢ;
  - no liability is in debit;
  - `available(seller) = Σ releases − Σ requested + Σ failed payouts`.
- **Contract:** REST against `ledger.yaml`, events against `api/events`.

## 10. Acceptance criteria

- [ ] Every posting in §3 is implemented exactly. The worked example is a passing test.
- [ ] The database enforces invariants 1–3. The integration tests prove that a violating write fails.
- [ ] The wallet API values always equal the derived sums. There is no stored balance column anywhere.
- [ ] Payouts never exceed `available`, even under concurrency.
- [ ] `server trial-balance` exits 0 on a consistent ledger and 1 otherwise.
- [ ] `go test ./...` and lint are green. `/readyz` checks Postgres and Kafka.
