# P2 WS1' — PR #6 review fixes

Source: the code review comment on PR #6 (2026-10-01). Branch `p2-ws1-payment`, scope `services/payment/**` plus this plan.
Each task: write the failing test first, make it pass, run `go test -race -count=1 ./...` inside `services/payment` (itests need Docker), `golangci-lint run ./services/payment/...` from the repo root, then commit `fix(payment): <summary>`.

## Blockers

- [x] **T1 — FAKE: a missing session is unpayable, not an outage (review #1).**
  `internal/adapter/fake/provider.go`: when `store.get` returns `app.ErrNotFound`, `Query` returns `domain.Observed{State: domain.ObservedExpired}` and `Cancel` returns `(false, nil)`. `Refund`/`QueryRefund`/`Submit` keep returning the error. Add a doc comment citing the port contract ("a session that cannot exist is unpayable").
  Tests: unit tests for Query/Cancel on an unknown payment id; an app- or itest-level test where a PENDING FAKE payment has no `fake_sessions` row and a new `CreatePayment` for that checkout succeeds (old row → EXPIRED, new attempt created) instead of returning `ErrProviderUnavailable`.

- [x] **T2 — Reconciler claims rows with a lease (review #2, spec §6 `FOR UPDATE SKIP LOCKED`).**
  Do NOT hold row locks across provider calls: `ApplyObserved` locks the checkout's rows in its own transaction and webhooks would block. Instead:
  - New migration `000004_review_fixes.up.sql` (+ down): `ALTER TABLE payments ADD COLUMN IF NOT EXISTS reconcile_after timestamptz NOT NULL DEFAULT '1970-01-01 00:00:00+00'` (done — the plan's `'-infinity'` is not scannable into Go's `time.Time` and broke every `SELECT *` over `payments`; the epoch is the same sentinel for the `reconcile_after <= @now` comparison).
  - Replace `ListPendingOlderThan` with `ClaimPendingForReconcile :many`:
    ```sql
    UPDATE payments SET reconcile_after = @lease_until
    WHERE id IN (
      SELECT id FROM payments
      WHERE status = 'PENDING' AND created_at < @before AND reconcile_after <= @now
      ORDER BY reconcile_after, created_at, id
      LIMIT @page_limit
      FOR UPDATE SKIP LOCKED)
    RETURNING *;
    ```
    `lease_until = now + Interval` (so a crashed replica's rows come back next interval). Ordering by `reconcile_after` first also stops the oldest 200 rows from starving the rest.
  - `make gen` / sqlc regenerate; update the `Store` port, postgres store, fakes and `RunOnce`. Check every `SELECT *`/`RETURNING *` consumer and the domain conversion still compile with the new column.
  - Tests: itest — two concurrent `ClaimPendingForReconcile` calls never return the same id; a claimed row is not returned again before its lease ends.

- [x] **T3 — VNPay 429/409 are retryable (review #3).**
  `internal/adapter/vnpay/provider.go` (the `switch` after `ReadAll`): wrap `>= 500`, `429` and `409` as `app.ErrProviderUnavailable`, mirroring the Stripe adapter's `wrap`.
  Tests: adapter stub returning 429 → `errors.Is(err, app.ErrProviderUnavailable)`; refund-worker test where the provider keeps answering 429 for more than `REFUND_MAX_ATTEMPTS` runs and the refund stays PENDING with `attempts` unchanged and no `RefundFailed` outbox row.

- [x] **T4 — One VNPay status mapper for querydr and the IPN (review #4).**
  Extract one helper used by both `Query` (`provider.go`) and `IPN` (`callback.go`): TS `00` with RC `00` → Succeeded; TS `01/05/06` → Pending; `07` → Pending + WARN; anything else → Failed with `Reason` = RC for IPN / TS for querydr (keep today's reasons). For an IPN that maps to Pending, do not call `ApplyObserved`; answer VNPay `RspCode 00` (received) and log at INFO — the reconciler resolves it.
  Tests: signed IPN with `vnp_ResponseCode=00, vnp_TransactionStatus=05` → payment stays PENDING, no outbox row, response `00`; same for `07`; existing success/failure IPN tests still pass.

- [x] **T5 — `GRPC_ADDR` is required (review #5).**
  `cmd/server/config.go` `validate`: add `{"GRPC_ADDR", c.GRPCAddr}` to `required`. Add the table case in `config_test.go` (and set it in the valid base config). Check `deploy/` compose and the README config table already set it; fix if not.

## Should fix (accepted)

- [x] **T6 — Indexes (review #6).** In `000004`: `CREATE UNIQUE INDEX IF NOT EXISTS refunds_provider_ref_uidx ON refunds (provider_refund_ref) WHERE provider_refund_ref IS NOT NULL;` and `CREATE INDEX IF NOT EXISTS provider_events_received_idx ON provider_events (received_at);`. Down migration drops them (and the T2 column). Extend `migrations_test.go` up/down. *Done as a plain partial index `refunds_provider_ref_idx`, not UNIQUE: the provider lives on `payments`, so uniqueness cannot be provider-scoped, and a cross-provider collision would wedge the refund worker on every update.*
- [x] **T7 — Refund worker reads the payment inside its tx.** Add `Tx.GetPayment` and use it at `internal/app/refunds.go` ~L216 instead of `w.store.GetPayment` (or reuse a row already returned by a lock held in that tx).
- [x] **T8 — Reconciler: one provider timeout per call.** In `reconcile.go`, give `queryAndApply` and `cancelAndObserve` their own `context.WithTimeout(ctx, providerTimeout)`. Test with a fake provider whose Query consumes most of the budget: the expired payment still reaches EXPIRED.
- [x] **T9 — Stripe wrong-amount webhook test.** Valid signature, `checkout.session.completed` with `amount_total` ≠ payment amount → payment stays PENDING, no `PaymentSucceeded` outbox row.
- [x] **T10 — Warn on P1-era `OrderCancelled`.** `refunds.go` ~L40: when `PaymentMethod == ""` or `CheckoutID == ""` (not for COD), log WARN with `event_id` and `order_id`. Inject a logger if `Refunds` lacks one.
- [x] **T11 — `go mod tidy`** in `services/payment` (moves `franz-go` to direct). Commit only go.mod/go.sum.

## Not doing (reasoning, recorded in README decisions)

- **Migration idempotency:** golang-migrate's pgx5 driver sends each file as one simple-protocol `Exec`, which Postgres runs as one implicit transaction, so a failure leaves nothing half-applied; on failure the version is marked dirty, which `IF NOT EXISTS` would not fix anyway. No change.
- **`claimStaleAfter` 31 m:** a create is bounded by the provider timeout, and the per-checkout advisory lock already prevents two concurrent creates; 31 m would make a crashed create block the buyer's retry for half an hour. Keep 2 m; fix the plan line that says 31 m and state the reason.
- **Deferred to follow-ups (user decision / cross-cutting):** refusing to start with FAKE outside dev (needs an `APP_ENV` marker across compose/e2e), a per-session token on the FAKE submit page, `processed_events` purge (belongs in `pkg/inbox`, shared with catalog).

## Final

- [x] Full `go test -race -count=1 ./...` in `services/payment`, lint clean, `make gen` leaves no diff, README updated (T2 lease, T4 IPN pending, decisions above). Do not push.
