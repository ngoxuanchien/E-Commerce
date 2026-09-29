# ADR 0008 — Payment provider port, webhook verification and idempotency

- Status: Accepted
- Date: 2026-09-29

## Context

P2 supports two real providers, VNPay (sandbox) and Stripe (test mode), and
CI can reach neither of them. The two providers differ in almost every way:

| | VNPay | Stripe |
|---|---|---|
| Hosted page | redirect URL | Checkout Session |
| Callback | IPN (`GET` with query parameters) | webhook (`POST`, JSON) |
| Callback signature | HMAC-SHA512 over the sorted parameters | `Stripe-Signature`, HMAC-SHA256 over timestamp and raw body |
| Amount | `amount × 100` | VND is zero-decimal |
| Status query | `querydr` | `Retrieve` |
| Cancel a session | not possible; the session expires | `sessions.expire` |

Both providers can deliver a callback twice or out of order. A callback can
also arrive after our own deadline. Locally VNPay's IPN can't reach a laptop,
and Stripe needs `stripe listen`.

## Decision

- **One port, three adapters.** payment-svc defines a `Provider` interface:
  `CreateSession`, `Query`, `Cancel`, `Refund`, `QueryRefund`. It has VNPay,
  Stripe and **FAKE** adapters.
  - The buyer picks a method (`VNPAY` or `STRIPE`), and configuration maps
    each method to its real adapter or to FAKE (`PROVIDER_VNPAY`,
    `PROVIDER_STRIPE`, default `fake`).
  - FAKE has a hosted page and a signed, delayed webhook. It goes through the
    same verification path, so CI exercises the real flow without network
    access.
- **Verify before parsing.** Every callback is checked before any state
  changes:
  - VNPay: the secure hash, then the terminal code, the reference and the
    amount;
  - Stripe: `webhook.ConstructEvent` over the raw body, with a 5 min
    tolerance;
  - FAKE: an HMAC-SHA256 signature in the Stripe style.

  The gateway forwards raw bodies and raw query strings unchanged, and tests
  pin that.
- **Callbacks are hints; `Query` is the truth.** A Stripe event triggers a
  fresh `Retrieve`, and the current state is applied, so delivery order
  doesn't matter. A verified VNPay IPN is authoritative for its own
  transaction.
- **Return URLs are never trusted for state.** A return URL with a valid
  signature only triggers a server-to-server `Query`. This is also how local
  VNPay works without a public IPN URL.
- **Idempotency** works at three layers:
  1. an inbox table `provider_events(provider, event_key)` drops duplicate
     deliveries;
  2. attempt state moves only forward. A terminal success never regresses,
     and a provider-confirmed success wins even over our own `EXPIRED` or
     `FAILED`, because the money has really moved;
  3. uniqueness guarantees at most one open attempt and one capturing
     payment per checkout.

  Outgoing provider calls carry idempotency keys: the payment id when
  creating a Stripe session, and the refund id when refunding.
- **Reconciliation.** A job queries attempts that are still pending after
  `RECONCILE_MIN_AGE`, and expires attempts that are past their deadline. It
  also re-checks refunds that are pending. So an attempt always reaches a
  terminal state, even without any callback.
- **Secrets** live in `deploy/secrets/payment.env` (gitignored). The real
  sandboxes are opt-in, and a runbook describes the manual check.

## Consequences

- Adding a provider means writing one adapter and one callback handler. The
  domain rules, the events and the ledger don't change.
- CI never depends on external sandboxes. The trade-off is that the real
  adapters are verified by adapter-level tests against httptest stubs with
  real signatures, plus a manual runbook check, rather than by the e2e
  suite.
- VNPay's string formats (hash field order, GMT+7 timestamps) are fragile.
  The implementation pins them with known test vectors and records the
  version of the documentation it followed.
- A Stripe session can't expire sooner than 30 minutes, so `PAYMENT_TIMEOUT`
  defaults to 30 minutes. Shorter deadlines are enforced by expiring the
  session explicitly.
