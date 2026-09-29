# WS6': P2 Integration and E2E

> Depends on WS1'–WS4' being merged; WS5' may land during WS6'. Owns `e2e/`, `docs/` (except `docs/superpowers/`), and root `README.md`. After WS1'–WS5' merge, WS6' also owns the Makefile `seed`/`e2e` targets and the CI `e2e` job. That is sequential with WS0', never parallel. This spec amends P1 `2026-09-28-marketplace-p1-ws6-integration.md`. The P1 rules still hold: fixes found here go back to the owning workstream as small PRs, and the WS6 gotchas (`GOWORK=off` for the e2e module, `COMPOSE_PROJECT_NAME` for fresh volumes, the login rate bucket) still apply.

## 1. Responsibility

WS6' proves the P2 success criteria (overview §1) on the real stack with the FAKE provider, extends the demo data, and documents payments, escrow and the ledger for portfolio readers. It also writes the runbook for the manual sandbox check (success criterion 7).

## 2. Fast timers for e2e

The e2e stack runs with shortened timers, passed as environment variables to `docker compose` (WS0' §8 exposes them as `${VAR:-default}`). `make e2e` and the CI job export them before `up-app`, while `make up-app` for demos keeps the defaults.

| Variable | e2e value |
|---|---|
| `PAYMENT_TIMEOUT` | `40s` |
| `SWEEPER_INTERVAL` | `2s` |
| `AUTO_COMPLETE_AFTER` | `5s` |
| `RECONCILE_INTERVAL` / `RECONCILE_MIN_AGE` | `2s` / `3s` |
| `FAKE_WEBHOOK_DELAY` | `300ms` |
| `PAYOUT_SETTLE_INTERVAL` / `PAYOUT_SETTLE_DELAY` | `1s` / `2s` |

Tests poll with deadlines derived from these values (the P1 `Eventually` style), and never sleep for a fixed time.

## 3. Seed data (`make seed`, extends P1)

Everything still goes through public APIs, with the FAKE provider driven by the hosted-page form (the same HTTP calls a browser makes). Re-running is idempotent. Each phase checks for existing data first, which also addresses the P1 seed-resume follow-up for the new phases. The new phases add:
- online-paid orders in several states: `AWAITING_PAYMENT`, paid and `PENDING_CONFIRMATION`, `SHIPPING`, `COMPLETED` (so seller 1 has a wallet balance), and cancelled after payment (`REFUNDED`);
- one settled payout for seller 1, so the wallet pages aren't empty;
- the printed summary now includes each seller's wallet figures.

## 4. End-to-end tests

**Go API tests** (`e2e/api/`, fast, CI):
1. **Online happy path:** checkout from 2 shops with `VNPAY` (FAKE) → `POST /api/payments` → submit the FAKE form (succeed) → both orders `PENDING_CONFIRMATION`/`PAID`. Then:
   - seller confirm, ship and deliver → wait `AUTO_COMPLETE_AFTER` → `COMPLETED` by `system`;
   - the seller wallet `available` grows by exactly `total − commission`, and `in_escrow` returns to its previous value.
2. **Stripe method** goes through the same flow with `STRIPE` (FAKE adapter). This proves the method routing.
3. **Failure then pay again:** FAKE `fail` → payment `FAILED`, orders still `AWAITING_PAYMENT` → a new attempt → success.
4. **Payment timeout:** no payment → after `PAYMENT_TIMEOUT` + sweeper → all orders `CANCELLED` (`PAYMENT_TIMEOUT`), and stock is restored (P1 polling helper).
5. **Refund on cancel:** pay, then the seller cancels one order at `CONFIRMED` → that order `REFUNDED`. The other order is unaffected. The ledger refund for that order's amount is visible in the trial balance.
6. **Duplicate and out-of-order callbacks:** FAKE hooks `duplicate` and `stale_after` → exactly one `PENDING_CONFIRMATION` transition, and the wallet is credited once after completion.
7. **Dropped webhook:** FAKE hook `drop` → the return URL and reconciler still mark the payment `SUCCEEDED` within `RECONCILE_INTERVAL` × 3.
8. **Double charge:** the first attempt uses `hook_cancel=unsupported`; its hosted page is kept open and submitted after the second attempt is paid → the second is `DUPLICATE` and refunded in full. The orders are paid once.
9. **Late success:** the attempt uses `hook_cancel=unsupported`. Let the deadline pass with the hosted page open, so the orders are cancelled, then submit → the payment `SUCCEEDED` and both orders are refunded.
10. **Payout:** request `available` → REQUESTED → SETTLED, and `available` = 0. Then request 1 more → 409 `INSUFFICIENT_FUNDS`. Two concurrent requests that together exceed `available` → exactly one succeeds.
11. **COD regression:** the P1 COD flow is unchanged, and no ledger postings are created.

After **every** test, a helper runs `docker compose exec -T ledger /server trial-balance` and asserts exit code 0 (total 0, no unbalanced transactions, no liability in debit).

**Playwright** (`e2e/playwright/`, real stack):
- buyer online checkout → FAKE page → result page → orders show "Đã thanh toán";
- failure → "Thanh toán lại" → success;
- seller wallet → payout → SETTLED.

**Observability:** extend the P1 trace test. The trace for `POST /api/payments` contains spans from `gateway`, `payment` and `order` (gRPC `GetCheckout`). The trace for the FAKE webhook contains `gateway` → `payment`, and the consumer spans in `order` and `ledger` continue it through Kafka `traceparent`.

## 5. Documentation

- **`docs/architecture.md`:**
  - add payment-svc and ledger-svc to the services table;
  - sequence diagrams for the online checkout, refund on cancel, escrow release and payout (Mermaid);
  - a "Money" section that summarizes the chart of accounts and links to ADR-0006 to ADR-0008.
- **`README.md`:** P2 features, the updated architecture diagram, how to switch to the real VNPay or Stripe sandboxes, and the new demo figures.
- **`docs/payments-sandbox.md` (new runbook)** for the manual, non-CI success criterion 7:
  - Stripe: `make stripe-listen`, test card, expected events.
  - VNPay: sandbox registration, the return-URL/querydr path without a tunnel, and the optional cloudflared IPN setup.
  - A checklist to record the date and the result of each run.
- **ADRs 0006–0008** were written with this spec. WS6' updates their Consequences section if the implementation deviates.

## 6. CI

Extend the P1 `e2e` job:
- export the fast timers (§2);
- build all six service images;
- `docker compose --profile app up -d --wait` (including `db-init`);
- `make seed`, then the Go API tests, then Playwright;
- on failure, upload the logs of `payment` and `ledger` together with the P1 artifacts.

The P1 follow-up says the CI e2e job has never run on GitHub. The P1.5 session must get it green before WS6' extends it (overview §7).

## 7. Acceptance criteria

- [ ] From a fresh clone: `make keys up-app seed` then `make e2e` passes locally and in CI.
- [ ] Every P2 success criterion 1–6 is demonstrated by an automated test (§4), and criterion 7 has a completed runbook entry for both providers.
- [ ] The trial balance is zero after every e2e test.
- [ ] `docs/architecture.md` and `README.md` describe P2 as built.
