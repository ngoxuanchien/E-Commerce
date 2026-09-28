# WS6 — Integration & E2E

> Depends on: WS1–WS4 merged (WS5 may land during WS6). Owns: `e2e/`, `docs/` (except `docs/superpowers/`), root `README.md`, and the `seed` / `e2e` Makefile targets.

## 1. Responsibility

Prove the P1 success criteria on the real stack, provide demo data, and document the system for portfolio readers. Fixes found here go back to the owning workstream's directory as small PRs (WS6 does not patch other workstreams' code directly).

## 2. Seed data (`make seed`)

A Go program `e2e/cmd/seed` that talks **only to public APIs through the gateway** (so it also exercises them):
- 3 sellers, each with a shop (e.g. fashion, electronics, home), 6–8 products each, 1–4 SKUs per product, images uploaded from `e2e/fixtures/images/` via presigned POST;
- 2 buyers;
- a few orders in different states (pending, confirmed, shipping, completed, cancelled).
Idempotent: re-running skips users/shops that already exist. Credentials printed at the end (synthetic accounts such as `seller1@example.test`).

## 3. End-to-end tests (`make e2e`)

Playwright (TypeScript) in `e2e/playwright/` against `make up-app` (real gateway + services + web):
1. **Happy path:** seller opens shop → creates a product with an image → buyer searches without diacritics → adds items from 2 shops → checkout → 2 orders → seller confirms/ships/delivers → buyer completes.
2. **Cancel returns stock:** buyer cancels a pending order → product stock back to its previous value (poll up to 5 s; release is async via Kafka).
3. **Oversell guard:** SKU with stock 1, two buyers check out concurrently (two browser contexts) → exactly one succeeds, the other sees the out-of-stock message.
4. **Auth:** session survives reload; logout blocks `/orders`; buyer cannot open `/seller/products`.
5. **Idempotency:** double-submitting checkout creates one set of orders.

API-level smoke tests (Go, `e2e/api/`) for the same flows run faster in CI and don't need a browser.

## 4. Observability check

A test fetches the Jaeger query API after a checkout and asserts a single trace contains spans from `gateway`, `order`, and `catalog` (gRPC `ReserveStock`).

## 5. Documentation

- `README.md`: what it is, architecture diagram, tech choices, quick start (`make keys up-app seed`), demo accounts, URLs (web, Jaeger, MinIO console), how to run tests.
- `docs/architecture.md`: services and ownership, data flow for checkout and cancellation (sequence diagrams in Mermaid), consistency approach (reservation, outbox, idempotency, sweeper).
- `docs/adr/`: short ADRs — 0001 microservices + database per service, 0002 gRPC internal + REST external, 0003 transactional outbox with Kafka, 0004 stock reservation strategy, 0005 JWT RS256 + refresh rotation.

## 6. CI

Extend `.github/workflows/ci.yml` with an `e2e` job: build images, `docker compose --profile app up -d --wait`, `make seed`, run Go API smoke tests and Playwright; upload Playwright report and service logs as artifacts on failure.

## 7. Acceptance criteria

- [ ] From a fresh clone: `make keys up-app seed` then `make e2e` passes locally and in CI.
- [ ] All six P1 success criteria in the overview are demonstrated by an automated test.
- [ ] README lets a reviewer run the demo in under 10 minutes.
