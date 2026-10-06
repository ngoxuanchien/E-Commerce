# ADR 0003 — Transactional outbox with Kafka

- Status: Accepted
- Date: 2026-09-28

## Context

Order state changes must be announced to other services. Most importantly,
a cancellation must return stock in catalog. Writing to Postgres and then
publishing to Kafka is a dual write: a crash between the two either loses
the event (stock is never returned) or publishes an event for a change that
rolled back.

## Decision

- order-svc writes each event into an `outbox` table in the **same
  transaction** as the state change. The table and relay come from
  `pkg/outbox`.
- A relay in the same binary polls unpublished rows with `FOR UPDATE SKIP
  LOCKED` (batch 100, every 500 ms). It publishes them to the Kafka topic
  `order.events` (3 partitions, key = `order_id`, so events for one order
  stay ordered), then sets `published_at`. Each batch first takes a
  transaction-scoped advisory lock, so when several relays share one outbox
  (replicas, or two app instances in a test) only one publishes at a time;
  `SKIP LOCKED` alone would let a second relay publish a newer event of an
  order before the first relay's older one.
- The writer's `traceparent` is stored with the row and sent as a Kafka
  header, so consumers continue the same trace.
- Delivery is at-least-once. Consumers are idempotent: catalog's
  `OrderCancelled` handler inserts the `event_id` into `processed_events` in
  the same transaction as the stock release and skips events it has already
  seen. It commits offsets only after successful processing and retries
  with backoff.
- Events use a versioned JSON envelope (`event_id`, `event_type`,
  `version`, `occurred_at`, `data`). JSON Schemas live in `api/events/`.

## Consequences

- There's no lost or phantom event, and no distributed transaction.
- Stock release after cancel is asynchronous: typically well under a
  second locally; the e2e tests allow 5 s.
- The outbox needs its own table and relay per producing service, and
  duplicates must be expected downstream.
