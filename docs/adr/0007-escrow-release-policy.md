# ADR 0007 — Escrow release policy

- Status: Accepted
- Date: 2026-09-29

## Context

Buyers pay online before the seller ships. A marketplace protects buyers by
holding the money until the order succeeds, and protects sellers by
releasing it reliably. It also has to handle cancellations, payment
deadlines, late provider callbacks and duplicate charges. Those come from
different services and can arrive in any order.

The alternatives considered:
- release on `DELIVERED`. The buyer has no window to object;
- release only when the buyer confirms. A buyer who forgets would hold the
  seller's money forever;
- hold until `COMPLETED`, and complete automatically after a grace period.

## Decision

1. **Hold until `COMPLETED`.** A successful payment is captured into one
   escrow account per order, split by the order totals of the checkout.
   ledger-svc releases an order's escrow only on `OrderCompleted`. The seller
   receives `total − commission`, and the platform receives the commission.
2. **Auto-complete.** order-svc's sweeper completes a `DELIVERED` order
   `AUTO_COMPLETE_AFTER` (default 72 h) after delivery, with actor `system`.
   A buyer can complete it earlier.
3. **Commission** is a flat rate of the subtotal (`COMMISSION_RATE_BPS`,
   default 500), applied only to online orders and snapshotted on the order
   at checkout. The seller keeps the shipping fee. COD orders never enter
   the ledger.
4. **Refund when captured and cancelled.** payment-svc creates a refund for
   an order **exactly when both facts hold**: the order's share has been
   captured, and the order has been cancelled. It checks the rule whenever
   either fact arrives, in the same transaction. So the rule covers, without
   any extra flow:
   - cancellation after payment;
   - a payment that succeeds after the payment deadline cancelled the order;
   - a cancellation racing a success.

   Refunds are for the full order amount. Partial refunds of an order are
   out of scope.
5. **The payment deadline never cancels a paid order.** Before cancelling
   `AWAITING_PAYMENT` orders, the sweeper asks payment-svc
   (`CancelCheckoutPayments`) to make every open attempt unpayable and to
   report whether one succeeded. If payment-svc can't answer, nothing is
   cancelled on that tick.
6. **Duplicate charges** are refunded in full. The first successful payment
   of a checkout is the capturing one. A later success is marked `DUPLICATE`
   and its money waits in the checkout's suspense account until the refund.
7. **Release and refund are mutually exclusive.** An order can only be
   cancelled up to `CONFIRMED` and only completes after `DELIVERED`. On top
   of that, the ledger refuses any posting that would push an escrow into
   debit.

## Consequences

- Sellers are paid 3 days after delivery at the latest. Buyers get their
  money back from any cancellation, even one that crosses a late callback.
- There are no refunds after `COMPLETED` (returns and disputes); that is
  deferred to a later phase.
- If payment-svc is down, orders can stay `AWAITING_PAYMENT` past their
  deadline, and stock stays reserved a little longer. This is preferred over
  cancelling a paid order.
- The rule is symmetric in arrival order, which makes it easy to test: the
  integration suites feed both orders and expect the same result.
