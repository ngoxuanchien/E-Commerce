# Marketplace P1 — Overview & Workstreams

> Umbrella spec. Each workstream has its own spec in this folder (`2026-09-28-marketplace-p1-ws*.md`).

## 1. Goal

A **learning / portfolio** project: a multi-vendor marketplace (a small Shopee) where many sellers open shops and buyers purchase from multiple shops. Stack: **Go microservices + React**.

**Decided:** portfolio purpose; Go + React; microservices; Phase 1 = core buying & selling.
**Assumptions:** P1 supports cash on delivery (COD) only; runs locally via Docker Compose; end-user UI copy is Vietnamese; money is VND stored as `int64`. All code and technical docs are in English.

**P1 success criteria** — after `make up-app && make seed`:
1. A buyer registers/logs in, browses and searches products (Vietnamese with or without diacritics), views details, and adds items from several shops to the cart.
2. Checkout splits the cart into **one order per shop**, paid by COD.
3. A user opens a shop → becomes a seller → lists products with variants (SKUs) and images.
4. The seller confirms → ships → marks delivered; the buyer confirms completion; cancelling an order returns stock.
5. Stock never goes negative / oversells under concurrent orders.
6. Automated tests pass in every service; a checkout request is traceable across gateway → order → catalog in Jaeger.

## 2. Roadmap

| Phase | Scope |
|---|---|
| **P1** | Identity, Catalog, Order (+ cart), Gateway, Web |
| P2 | Payment service (VNPay/Stripe sandbox), escrow, seller wallet, commission |
| P3 | Reviews/ratings, Notification service (email + WebSocket) |
| P4 | Admin panel (shop/product moderation, categories, analytics) |
| P5 | Search service (Elasticsearch), buyer–seller chat, K8s/Helm |

Out of scope for P1: online payment, real shipping/dynamic shipping fees, vouchers, reviews, notifications, admin, password reset/email verification, saved address book.

## 3. Architecture

```
Browser ─► web (React/Vite) ─/api─► gateway :8080
                                      │  verify JWT (JWKS), strip & inject X-User-Id / X-User-Roles
                                      ├─► identity-svc :8081 (gRPC :9081) ─► Postgres identity_db
                                      ├─► catalog-svc  :8082 (gRPC :9082) ─► Postgres catalog_db, MinIO
                                      └─► order-svc    :8083 (gRPC :9083) ─► Postgres order_db, Redis

catalog-svc ──gRPC GrantRole──► identity-svc
order-svc   ──gRPC GetSkus / ReserveStock / ReleaseStock──► catalog-svc
order-svc   ──outbox → Kafka topic `order.events`──► catalog-svc (consumer: OrderCancelled → release stock)
All services ──OTLP──► Jaeger
```

- **Database per service** (one Postgres instance, three separate databases; no service reads another's DB).
- **Synchronous:** REST externally through the gateway; gRPC between services.
- **Asynchronous:** Kafka (single-node KRaft) + transactional outbox, idempotent consumers.
- Services trust `X-User-*` headers because only the gateway publishes a port to the host.

## 4. Shared conventions (details in WS0)

- IDs: UUIDv7 strings. Timestamps: UTC, RFC 3339. Money: `int64` VND.
- REST errors: `{"error":{"code":"OUT_OF_STOCK","message":"...","details":{...}}}`.
- Every service exposes `/healthz` (liveness) and `/readyz` (dependencies ready) and runs migrations on startup.
- Go workspace with multiple modules (`pkg`, `gen`, one per service) so workstreams never touch each other's `go.mod`.

## 5. Workstreams

**Contract-first:** WS0 fixes every interface (OpenAPI, proto, event schemas, shared `pkg/`, service skeletons, compose). Once WS0 is merged, WS1–WS5 run **in parallel** (one branch/worktree each, only editing the directories they own, depending on contracts rather than on each other's code). WS6 integrates at the end.

```
WS0 Foundation & Contracts
   ├── WS1 identity ──┐
   ├── WS2 catalog  ──┤
   ├── WS3 order    ──┼──► WS6 Integration & E2E
   ├── WS4 gateway  ──┤
   └── WS5 web      ──┘
```

| WS | Spec | Owns | Mocks during development |
|---|---|---|---|
| WS0 | `...-ws0-foundation.md` | `go.work`, `pkg/`, `gen/`, `proto/`, `api/`, `deploy/`, `Makefile`, `.github/`, `services/*` skeletons | — |
| WS1 | `...-ws1-identity.md` | `services/identity/` | — |
| WS2 | `...-ws2-catalog.md` | `services/catalog/` | fake Identity gRPC |
| WS3 | `...-ws3-order.md` | `services/order/` | fake Catalog gRPC |
| WS4 | `...-ws4-gateway.md` | `services/gateway/` | httptest upstreams + JWKS stub |
| WS5 | `...-ws5-web.md` | `web/` | MSW handlers from OpenAPI |
| WS6 | `...-ws6-integration.md` | `e2e/`, `docs/`, README | — (real stack) |

**Coordination rules**
- Contract changes (`api/`, `proto/`, `pkg/`) go through a dedicated PR to `main`; `buf breaking` blocks breaking changes; in-flight workstreams rebase afterwards.
- A workstream merges when its module's tests + lint are green and it meets the acceptance criteria in its spec.
- WS6 starts once WS1–WS4 are merged; WS5 may merge before or during WS6.
