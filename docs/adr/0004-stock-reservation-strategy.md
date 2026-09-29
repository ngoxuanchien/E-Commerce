# ADR 0004 — Stock reservation strategy

- Status: Accepted
- Date: 2026-09-28

## Context

Stock lives in catalog; orders live in order-svc. A checkout can contain
items from several shops and becomes one order per shop. It must never
oversell, even with many buyers racing for the last unit, and it must be all
or nothing: either every order of a checkout is placed, or none is.

The alternatives considered:
- reserve on seller confirmation, which oversells at checkout;
- lock SKUs across services for the whole checkout, which holds locks
  across network calls;
- reserve synchronously during checkout, with compensation.

## Decision

Reserve synchronously during checkout, and compensate on failure.

1. order-svc inserts all orders as `PENDING` first (Tx A), so a crash later
   is recoverable.
2. For each order it calls `ReserveStock(order_id, items)`, with 3 s per
   attempt and retries on `UNAVAILABLE`/`DEADLINE_EXCEEDED`. Catalog
   decrements stock in one transaction, using a conditional
   `UPDATE … WHERE stock >= q AND active` in sorted SKU order, and records
   the reservation per `(order_id, sku_id)`. It's idempotent per order, so
   retries are safe.
3. If any reservation fails, order-svc calls `ReleaseStock` for every order
   of the checkout (also idempotent), cancels them all (Tx B, with events,
   so the async consumer releases stock even if the sync call failed), and
   answers `409 OUT_OF_STOCK` with the shortages.
4. If all succeed, every order moves to `PENDING_CONFIRMATION` (Tx C).
5. A sweeper cancels and releases orders stuck in `PENDING` for longer than
   2 minutes (for example, after a crash between Tx A and Tx C).
6. Later cancellations return stock asynchronously through `OrderCancelled`
   (ADR 0003).

## Consequences

- No locks are held across services; the database's `stock >= 0` constraint
  and conditional updates make overselling impossible. The e2e suite checks
  this with concurrent buyers.
- Stock is taken at checkout, not at confirmation, so abandoned or cancelled
  orders hold stock until they're cancelled.
- A known gap remains: a seller's product edit sets on-hand stock directly,
  so an edit form opened before a sale can re-inflate stock. Fixing it
  needs an `expected_stock`/version field in the catalog contract, which is
  deferred.
