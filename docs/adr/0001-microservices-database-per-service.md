# ADR 0001 — Microservices with a database per service

- Status: Accepted
- Date: 2026-09-28

## Context

The marketplace is a portfolio project whose main goal is to show service
boundaries, inter-service communication and consistency handling in Go. The
domain splits naturally into identity (accounts, tokens), catalog (shops,
products, stock) and order (cart, checkout, lifecycle). Workstreams were
developed in parallel, each owning one directory.

## Decision

Build Phase 1 as four Go services (gateway, identity, catalog, order) plus a
React web app. Each stateful service owns its own database: `identity_db`,
`catalog_db` and `order_db` live on one Postgres instance, and no service
reads another's database. Order keeps carts in Redis; catalog stores images
in MinIO. Services interact only through their published contracts
(REST through the gateway, gRPC internally, Kafka events).

## Consequences

- There are no cross-database joins. Orders snapshot what they need from
  the catalog (product name, variant, image, price, shop name) at checkout,
  so later catalog edits never change past orders.
- Operations that span services cannot use one transaction. Checkout uses a
  reserve/compensate flow (ADR 0004), and cross-service side effects use
  events with an outbox (ADR 0003), so stock release after a cancellation is
  eventually consistent.
- Each service runs its own migrations on startup, and each can be tested in
  isolation against fakes of its neighbours (bufconn gRPC fakes, httptest
  upstreams).
- Running everything locally needs Docker Compose with several
  infrastructure containers; `make up-app` hides that.
