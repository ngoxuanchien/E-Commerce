# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# Project

Shopee-style multi-vendor marketplace (portfolio/learning project): Go microservices + React SPA, run locally with Docker Compose. P1 (identity, catalog, order/cart, gateway, web, COD) is done; P2 adds online payment (VNPay/Stripe sandbox), escrow and a seller wallet. Repo files, specs and comments are written in English.

## Commands

```bash
make keys up-app seed   # dev JWT key, build + start full stack, seed demo data (password: matkhau123)
make up                 # infra only (Postgres, Redis, Kafka, MinIO, Jaeger)
make down
make test               # go test -race in every module (testcontainers → needs Docker)
make test-short         # same, skipping container tests (pkg/testutil skips on -short)
make lint               # golangci-lint per module + buf lint/format + Redocly (OpenAPI)
make gen                # buf generate (proto → gen/) + sqlc generate (per service)
make e2e                # API smoke tests + Playwright against a running, seeded stack
```

Single Go test — run inside the module directory (each service is its own Go module, tied together by `go.work`):

```bash
cd services/order && go test -race -count=1 -run TestCheckout ./internal/app/
```

`e2e/` is **not** in `go.work`; always run it with `GOWORK=off` (as the Makefile does).

Web (`web/`): `npm run dev` (or `dev:mock` with MSW mocks, no backend), `npm test` (Vitest), `npx vitest run <file>`, `npm run lint` (checks generated API types are current + eslint + tsc), `npm run gen:api` after an OpenAPI change, `npm run e2e` (Playwright on mocked APIs).

CI fails if `make gen` changes anything under `gen/` or `services/`, and runs `buf breaking` against `origin/main` — commit regenerated code and don't break proto compatibility.

## Architecture

REST outside, gRPC inside, Kafka for events, one Postgres database per service (one Postgres instance, separate DBs; never read another service's DB).

| Service | HTTP / gRPC | Owns |
|---|---|---|
| gateway | 8080 / – | single public entry: JWT (RS256 via JWKS), routing, CORS, rate limits |
| identity | 8081 / 9081 | users, login, refresh rotation, roles (`GrantRole`) |
| catalog | 8082 / 9082 | shops, products/SKUs, images (MinIO presigned POST), search, **inventory (source of truth for stock)** |
| order | 8083 / 9083 | cart (Redis), checkout (one order per shop), order state machine, sweeper |
| payment | 8084 / 9084 | P2: VNPay/Stripe/FAKE providers behind a port, webhooks |
| ledger | 8085 / 9085 | P2: double-entry append-only ledger, escrow, seller wallet |
| web | 3000 | React SPA via nginx, proxies `/api` to the gateway |

Cross-cutting design (details in `docs/architecture.md` and `docs/adr/`):
- **Trust boundary:** the gateway strips incoming `X-User-Id`/`X-User-Roles` and injects verified ones; services trust these headers (only the gateway is exposed) but still enforce ownership (another user's resource → 404).
- **Events:** state changes are written to a transactional **outbox** in the same DB tx and relayed to Kafka (`order.events`, `payment.events`, ...; schemas in `api/events/`). Consumers dedupe through an inbox / processed-events table (`pkg/inbox`).
- **Checkout** (order-svc): idempotency key claim → Tx A insert orders → reserve stock via catalog gRPC → Tx B compensate (release + cancel) or Tx C confirm. A sweeper cleans up stale/expired orders with `FOR UPDATE SKIP LOCKED`. See `services/order/README.md`.
- **Money** is never touched by order-svc directly: it calls payment-svc (`PaymentInternal`); balances live only in ledger-svc as sums of entries.

### Layout conventions

- Contracts first: `api/openapi/*.yaml` (REST), `proto/` (gRPC → generated into `gen/`), `api/events/*.schema.json`. Change the contract, regenerate, then implement.
- `pkg/` holds shared libraries (config, server, httpx, grpcx, outbox, inbox, kafkax, idemkey, pgxx, otelx, logx, testutil).
- Each service is hexagonal: `internal/domain` (pure rules) → `internal/app` (use cases + port interfaces) → `internal/adapter/{http,grpc,postgres,redis,kafka,...}`; `internal/bootstrap` wires it for both `cmd/server` and the `internal/itest` end-to-end suite. Fakes for other services live next to their client adapter (e.g. `adapter/catalog/catalogfake`, bufconn).
- SQL: write queries in `internal/adapter/postgres/queries/*.sql`, run `make gen-sqlc`; migrations are golang-migrate files in `services/<svc>/migrations/`, embedded via `migrations.go`.
- Each service's `README.md` documents config env vars and its design decisions; specs and plans per workstream are in `docs/superpowers/specs/` and `docs/superpowers/plans/`.

### Gotchas

- `no migration found for version N` on startup: the shared Postgres volume holds migrations from another branch — check out `main` or `make down && docker volume rm marketplace_pgdata`.
- The gateway rate-limits login/register to 10/min per IP; seed and e2e clients wait on `Retry-After`.

# Behavioral guidelines

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.
