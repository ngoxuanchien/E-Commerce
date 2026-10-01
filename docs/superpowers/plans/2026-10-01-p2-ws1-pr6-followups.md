# P2 WS1' — PR #6 deferred follow-ups (mock-testable only)

This is a learning project and is never deployed to prod. Prod-only hardening is skipped on purpose: refusing to start with FAKE outside dev, and a token on the FAKE submit page.
These tasks can all be built and verified with fakes and testcontainers.
Branch `p2-ws1-payment`.

Each task does the following:
1. Write the failing test first.
2. Make it pass.
3. Run `go build ./... && go vet ./... && go test -race -count=1 ./...` in the touched module(s).
4. Run `./bin/golangci-lint run ./<module>/...` from the repo root.
5. Make one commit, ticking the box in the same commit.

## F1 — purge `processed_events` (the deferred `pkg/inbox` item)

- [x] **F1a — `pkg/inbox`: `PurgeBefore`.**
  - Add to `pkg/inbox/inbox.go`:
    ```go
    // PurgeBefore deletes markers processed before t and reports how many went. A redelivery older
    // than the retention window would be processed again, so t must be far older than any broker
    // retention or retry horizon.
    func PurgeBefore(ctx context.Context, db Execer, t time.Time) (int64, error)
    ```
    It runs `DELETE FROM processed_events WHERE processed_at < $1`. Use whatever `Execer` already exposes; if it lacks `Exec` returning a `pgconn.CommandTag`, extend it minimally.
  - Tests in `pkg/inbox/inbox_test.go`: insert two markers, back-date one with `UPDATE … SET processed_at = now() - interval '40 days'`, purge with `now - 30 days`, and check that 1 row is deleted and the recent marker stays (`MarkProcessed` on it still answers `false`).
  - Add an index in `pkg/inbox/schema.sql`: `CREATE INDEX IF NOT EXISTS processed_events_processed_at_idx ON processed_events (processed_at);`. `TestSchemaIsIdempotent` must still pass.
  - Commit: `feat(pkg): inbox.PurgeBefore`.

- [x] **F1b — payment: the reconciler purges `processed_events` older than 30 days.**
  - Add `PurgeProcessedEventsBefore(ctx, t) (int64, error)` to the `Store` port (`internal/app/ports.go`).
  - The postgres store implements it with `inbox.PurgeBefore` on its pool.
  - `RunOnce` in `internal/app/reconcile.go` calls it after the provider-events purge, with the same `providerEventTTL` (30 days), or a new const `processedEventTTL = 30 * 24 * time.Hour` if that reads clearer.
  - Add the same index in migration `000004_review_fixes.up.sql`, with a matching DROP in the down migration. `migrations_test.go` asserts it, the same way T6 asserts its indexes.
  - Tests:
    - a store test for the purge;
    - a reconciler test checking that a back-dated `processed_events` row is gone after `RunOnce` and a fresh one remains.
  - Commit: `fix(payment): the reconciler purges processed_events after 30 days`.

- [ ] **F1c — move the `processed_events` index out of the already-pushed `000004`.**
  - Why: `000004` was pushed in 8459b27, and a database already at version 4 never re-runs it, so the index F1b added there would never reach it.
  - Remove the index and its DROP from `000004_review_fixes.{up,down}.sql`.
  - Add `000005_processed_events_purge.up.sql` with `CREATE INDEX IF NOT EXISTS processed_events_processed_at_idx ON processed_events (processed_at);` and a comment on why.
  - Add the matching `.down.sql` (`DROP INDEX IF EXISTS …`).
  - Keep the copy in `000002`. The verbatim-schema test needs it, and `IF NOT EXISTS` makes it harmless.
  - Update `migrations_test.go` so the down sequence runs 000005 first. The applied DB must have the index, and it must be gone after the downs.
  - Commit: `fix(payment): processed_events index in its own migration`.

Catalog has no periodic worker, so it is not wired here. It can call `inbox.PurgeBefore` once it has one; that is recorded in the follow-ups memory.

## F2 — `cancelAndObserve`: Cancel and its follow-up Query each get their own timeout

- [ ] **F2.**
  - `internal/app/cancel.go`: `cancelAndObserve` currently runs `prov.Cancel` and `queryAndApply` under one `WithTimeout(ctx, providerTimeout)`.
    - Give it a `timeout time.Duration` parameter.
    - Wrap `Cancel` and `queryAndApply` in separate `context.WithTimeout(ctx, timeout)` calls, calling every cancel func.
  - Callers:
    - The `Canceller` passes `providerTimeout`.
    - The reconciler (`reconcile.go`) passes `r.cfg.ProviderTimeout` and **drops** the outer `withProviderTimeout` wrapper around `cancelAndObserve`. Otherwise the parent deadline still caps both calls.
    - `queryAndApply` in the reconciler keeps its own wrapper.
  - Test (fake provider, short timeout through `ReconcileConfig.ProviderTimeout`; the same style as T8's "a slow Query leaves Cancel its own provider budget"):
    - Cancel takes about 3/4 of the budget.
    - The follow-up Query fails with `context.DeadlineExceeded` if less than half the budget is left.
    - An expired PENDING payment still reaches EXPIRED.
    - Make sure the test fails before the change.
  - Commit: `fix(payment): Cancel and its follow-up Query get separate timeouts`.

## Final

- [ ] README: add one line under decision 22 (per-call timeouts now also cover Cancel and Query inside `cancelAndObserve`) and one under the dedupe/retention notes (`processed_events` is purged after 30 days). Make sure `make gen` leaves no diff, the full test suite passes, and lint is clean. Commit: `docs(payment): follow-ups F1–F2`.
