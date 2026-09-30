# ledger-svc

The double-entry, append-only ledger of the marketplace (P2 WS2'). It is the single source of truth for money positions:
- provider clearing and checkout suspense;
- per-order escrow;
- seller wallets and payouts;
- platform commission.

Every balance is derived by summing entries. No balance is stored anywhere.

- HTTP `:8085`: `/healthz`, `/readyz` (Postgres, Kafka) and the seller wallet API (`api/openapi/ledger.yaml`).
- gRPC `:9085`: the standard health service only.
- Consumes `payment.events` and `order.events` (group `ledger-svc`). Publishes `payout.events` (key `seller_user_id`) through the outbox.
- Spec: `docs/superpowers/specs/2026-09-29-marketplace-p2-ws2-ledger.md`. ADRs 0006 and 0007.

## Layout

| Package | Role |
|---|---|
| `internal/domain` | Chart of accounts, `Journal` (Σ = 0 check) and one builder per posting kind |
| `internal/app` | `Post` (lock → replay check → overdraw check → insert), `EventHandler`, `PendingJob`, `PayoutService`, `SettleJob`, `WalletService`, `TrialBalance` |
| `internal/adapter/postgres` | sqlc queries (`queries/` → `sqlcdb/`, regenerate with `make gen-sqlc`) |
| `internal/adapter/kafka` | Consumer for both topics |
| `internal/adapter/http` | `ledger.yaml` handlers |
| `internal/adapter/bank` | `BankPort` simulator (always succeeds) |
| `internal/bootstrap` | Wiring shared by `cmd/server` and the end-to-end test |

## Invariants and where they are enforced

| Invariant | Database | Code |
|---|---|---|
| Every transaction has ≥ 2 entries summing to 0 | Deferred constraint triggers `entries_balanced`, `journal_transactions_have_entries` | `Journal.Validate` |
| Append-only (`journal_transactions`, `entries`, `escrow_allocations`) | `BEFORE UPDATE/DELETE/TRUNCATE` triggers | Nothing ever updates them |
| No liability account in debit | Deferred trigger `entries_liability_not_in_debit` (backstop) | `Post` locks debited liability accounts `FOR UPDATE` in id order and re-checks the balance |
| Each posting happens once | `UNIQUE (idempotency_key)` | `Post` returns the existing transaction; consumers also record `processed_events` in the same transaction |

## Decisions

- **Permanent vs retryable events.** Events that can never apply are logged at ERROR and skipped, so they never block a partition (`kafkax` retries errors forever):
  - undecodable events, bad ids, invalid amounts and unknown providers;
  - allocations above the payment amount;
  - invariant rejections, such as a refund of a released escrow (`alert=true`).

  Only infrastructure errors and a refund that arrives before its capture are retried.
- **Release and refund amounts come from `escrow_allocations`,** which record what was captured. When an event disagrees, the difference is logged.
- **A second payment naming already-captured orders** keeps its `PAY`, so the money sits in suspense for the duplicate refund. Its capture is skipped with an alert.
- **Payouts:**
  - the minimum (`PAYOUT_MIN_AMOUNT`) is checked before the `Idempotency-Key` claim;
  - `201` and `409 INSUFFICIENT_FUNDS` are both stored and replayed;
  - a claim orphaned by a crash can be reclaimed after 1 minute.
- **An order-scoped refund whose `payment_id` differs from the payment that captured the order** is skipped with `alert=true`.
- **The pending-postings job claims untouched rows first (`last_attempt_at NULLS FIRST`),** so rows that never get a capture can't starve newer ones.
- **Idempotent payout replays return JSON-equal bodies** (the stored response is `jsonb`, so key order and whitespace may differ).
- **`platform_cash` goes negative** as payouts settle, because P2 doesn't model provider settlement (ADR-0006).

## Operations

```bash
docker compose -f deploy/docker-compose.yml exec ledger /server trial-balance
```

It prints `{balances_by_type, total, unbalanced_transactions, liabilities_in_debit, ok}`. It exits 0 when the ledger is consistent and 1 otherwise, including when the database is unreachable.

## Configuration

`PAYOUT_MIN_AMOUNT` (50000), `PAYOUT_SETTLE_INTERVAL` (30s), `PAYOUT_SETTLE_DELAY` (1m) and `PENDING_POSTING_INTERVAL` (10s), plus the common variables (`SERVICE_NAME`, `HTTP_ADDR`, `GRPC_ADDR`, `DATABASE_URL`, `KAFKA_BROKERS`, …).

## Tests

- `go test -short ./...` runs the unit tests.
- `go test ./...` also starts Postgres and Kafka containers. It covers:
  - the triggers, the posting engine and the event handlers;
  - payouts under concurrency;
  - HTTP contract tests against `ledger.yaml`;
  - event schemas against `api/events`;
  - the in-process end-to-end test and the `rapid` property test (`-rapid.checks=N` to tune).

## Known gaps

- `idempotency_keys` rows are never purged (`idemkey.Purge` is not scheduled yet).
- An order-scoped `PaymentRefunded` that arrives before its capture is retried (spec §6). If that capture was skipped as malformed, the refund retries forever and stalls its `payment.events` partition; recovering it needs manual intervention.
- If any order of a payment was already captured by another payment, the whole CAPTURE of the new payment is skipped (alert=true); its orders' money stays in suspense and their completions stay pending until someone intervenes.
- `trial-balance` scans every entry; fine at portfolio scale.
- `pkg/idemkey.Release` deletes a key even after it was completed. If a payout's COMMIT succeeds but the client sees an error, a retry with the same key could create a second payout. The fix, guarding `Release` with `status_code IS NULL`, belongs to WS0' (`pkg/`).
