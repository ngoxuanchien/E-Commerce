# WS0 — Foundation & Contracts

> Overview: `2026-09-28-marketplace-p1-overview.md`. WS0 must merge first; every other workstream relies only on what WS0 defines here.

## 1. Goal

Provide **everything the other workstreams need to proceed in parallel without waiting on each other**: repo layout, contracts (REST/gRPC/events), shared libraries, runnable skeletons for the four services, local infrastructure, and CI.

## 2. Repo layout & modules

```
go.work                          # use ./pkg ./gen ./services/{gateway,identity,catalog,order}
pkg/          (module .../pkg)   # shared libraries
gen/          (module .../gen)   # code generated from proto (committed)
proto/                           # buf.yaml, buf.gen.yaml, catalog/v1, identity/v1
api/openapi/                     # identity.yaml, catalog.yaml, order.yaml, common.yaml
api/events/                      # JSON Schemas for Kafka events
services/<svc>/  (own module)    # cmd/server/main.go, internal/, migrations/, Dockerfile
deploy/docker-compose.yml, deploy/postgres/init.sql
Makefile, .github/workflows/ci.yml, .gitignore, .editorconfig
```

Root module path: `github.com/ngoxuanchien/e-commerce/<dir>`. Go: latest stable release at WS0 time, pinned in every `go.mod`. Tools (`buf`, `sqlc`, `golangci-lint`, `migrate`) are pinned via `tool` directives in `pkg/go.mod` or fixed versions in the Makefile.

## 3. Conventions

| Topic | Convention |
|---|---|
| IDs | UUIDv7, strings in JSON |
| Time | UTC, RFC 3339 (`2026-09-28T10:00:00Z`) |
| Money | `int64` VND, no decimals |
| JSON | `snake_case` |
| Pagination | `?limit=` (default 20, max 100) + `?cursor=` (opaque); response `{ "items": [...], "next_cursor": "..." \| null }` |
| Errors | `{"error":{"code","message","details"}}` |
| Internal headers | `X-User-Id`, `X-User-Roles` (comma-separated), `X-Request-Id`, `traceparent` |
| Ports | gateway 8080; identity 8081/9081; catalog 8082/9082; order 8083/9083 (HTTP/gRPC) |
| Env | `SERVICE_NAME`, `HTTP_ADDR`, `GRPC_ADDR`, `DATABASE_URL`, `REDIS_URL`, `KAFKA_BROKERS`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `LOG_LEVEL` + service-specific vars |

**Error codes:** `VALIDATION_ERROR` 400 · `UNAUTHENTICATED` 401 · `FORBIDDEN` 403 · `NOT_FOUND` 404 · `CONFLICT` 409 · `OUT_OF_STOCK` 409 · `INVALID_STATE_TRANSITION` 409 · `IDEMPOTENCY_KEY_REUSED` 422 · `RATE_LIMITED` 429 · `INTERNAL` 500 · `UPSTREAM_UNAVAILABLE` 503.

## 4. REST contract (OpenAPI 3.1 in `api/openapi/`)

Public paths through the gateway. **Auth** column: `–` public, `U` logged-in user, `S` requires role `seller`.

### identity.yaml
| Method & path | Auth | Body → Response |
|---|---|---|
| `POST /api/auth/register` | – | `{email, password, full_name}` → 201 `User` |
| `POST /api/auth/login` | – | `{email, password}` → `TokenResponse` + sets cookie `refresh_token` |
| `POST /api/auth/refresh` | – (cookie) | → `TokenResponse` + new cookie |
| `POST /api/auth/logout` | – (cookie) | → 204, clears cookie |
| `GET /api/users/me` | U | → `User` |

`User {id, email, full_name, roles[], created_at}` · `TokenResponse {access_token, token_type:"Bearer", expires_in, user: User}`.
Internal only (not routed by the gateway): `GET /.well-known/jwks.json`.

### catalog.yaml
| Method & path | Auth | Body → Response |
|---|---|---|
| `GET /api/categories` | – | → `Category[]` (two-level tree) |
| `GET /api/products` | – | query `q, category, shop, min_price, max_price, sort=relevance\|newest\|price_asc\|price_desc, limit, cursor` → page of `ProductSummary` |
| `GET /api/products/{id}` | – | → `Product` |
| `GET /api/shops/{slug}` | – | → `Shop` |
| `POST /api/shops` | U | `{name, slug, description}` → 201 `Shop` (user becomes a seller; the client must call `/api/auth/refresh` to get a token with the new role) |
| `GET /api/shops/me` · `PATCH /api/shops/me` | S | → `Shop` |
| `GET /api/seller/products` | S | query `status, limit, cursor` → page of `Product` |
| `POST /api/seller/products` | S | `ProductInput` → 201 `Product` |
| `PATCH /api/seller/products/{id}` | S | `ProductInput` (partial) → `Product` |
| `DELETE /api/seller/products/{id}` | S | → 204 (archives) |
| `POST /api/seller/uploads` | S | `{content_type}` → `{upload_url, fields, object_key, public_url}` (presigned POST) |

`Shop {id, name, slug, description, owner_user_id, created_at}` · `Category {id, name, slug, children[]}` · `Sku {id, name, price, stock, active}` · `Product {id, shop:{id,name,slug}, category_id, name, description, status: draft|active|archived, images:[{object_key,url}], skus: Sku[], min_price, created_at, updated_at}` · `ProductSummary {id, name, image_url, min_price, shop:{id,name,slug}}` · `ProductInput {category_id, name, description, status, image_keys[], skus:[{id?, name, price, stock, active}]}`.

### order.yaml
| Method & path | Auth | Body → Response |
|---|---|---|
| `GET /api/cart` | U | → `Cart` |
| `PUT /api/cart/items/{sku_id}` | U | `{quantity}` (1–99) → `Cart` |
| `DELETE /api/cart/items/{sku_id}` | U | → `Cart` |
| `POST /api/checkout` | U | header `Idempotency-Key`; `{sku_ids?[], shipping_address: Address, payment_method:"COD"}` → 201 `{checkout_id, orders: Order[]}` |
| `GET /api/orders` | U | query `status, limit, cursor` → page of `Order` (buyer's own) |
| `GET /api/orders/{id}` | U | → `Order` (buyer or seller of that order) |
| `POST /api/orders/{id}/cancel` | U | `{reason}` → `Order` |
| `POST /api/orders/{id}/complete` | U | → `Order` |
| `GET /api/seller/orders` | S | query `status, limit, cursor` → page of `Order` |
| `POST /api/seller/orders/{id}/{confirm\|ship\|deliver\|cancel}` | S | `{reason?}` → `Order` |

`Cart {shops:[{shop:{id,name,slug}, items:[CartItem]}], subtotal}` · `CartItem {sku_id, product_id, product_name, variant_name, image_url, unit_price, quantity, stock, available}` · `Address {full_name, phone, line1, ward, district, province}` · `Order {id, checkout_id, status, buyer_id, shop:{id,name}, items:[OrderItem], subtotal, shipping_fee, total, shipping_address, payment_method, cancel_reason, history:[{from,to,actor_role,note,created_at}], created_at, updated_at}` · `OrderItem {sku_id, product_id, product_name, variant_name, image_url, unit_price, quantity, line_total}`.

All error responses use the `Error` schema in `common.yaml`. OpenAPI is the **source of truth** for WS5 (TypeScript types are generated from it) and for WS1–WS3 (handlers must match; integration tests validate requests/responses with `kin-openapi`).

## 5. gRPC contract (`proto/`, buf, generated into `gen/`)

```proto
// proto/identity/v1/identity.proto
service IdentityInternal {
  rpc GrantRole(GrantRoleRequest) returns (GrantRoleResponse); // idempotent
}
message GrantRoleRequest { string user_id = 1; string role = 2; }  // role: "seller"
message GrantRoleResponse {}

// proto/catalog/v1/catalog.proto
service CatalogInternal {
  rpc GetSkus(GetSkusRequest) returns (GetSkusResponse);
  rpc ReserveStock(ReserveStockRequest) returns (ReserveStockResponse); // idempotent per order_id
  rpc ReleaseStock(ReleaseStockRequest) returns (ReleaseStockResponse); // idempotent; no-op if nothing reserved
}
message GetSkusRequest { repeated string sku_ids = 1; }
message GetSkusResponse { repeated SkuInfo skus = 1; }   // unknown SKUs are omitted
message SkuInfo {
  string sku_id = 1; string product_id = 2; string product_name = 3; string variant_name = 4;
  string image_url = 5; int64 price = 6; int32 stock = 7; bool available = 8; // sku.active && product.active
  string shop_id = 9; string shop_name = 10; string seller_user_id = 11;
}
message StockItem { string sku_id = 1; int32 quantity = 2; }
message ReserveStockRequest { string order_id = 1; repeated StockItem items = 2; }
message ReserveStockResponse {}
message ReleaseStockRequest { string order_id = 1; }
message ReleaseStockResponse {}
```

gRPC errors: `NOT_FOUND`, `INVALID_ARGUMENT`; insufficient stock → `FAILED_PRECONDITION` with an `InsufficientStock { repeated StockShortage shortages }` detail (`sku_id, requested, available`), defined in `catalog.proto`.

## 6. Event contract (`api/events/`, Kafka)

Topic `order.events` (3 partitions, key = `order_id`). JSON envelope:
```json
{ "event_id": "uuidv7", "event_type": "OrderCancelled", "version": 1,
  "occurred_at": "2026-09-28T10:00:00Z", "data": { } }
```
| event_type | data |
|---|---|
| `OrderCreated` | `order_id, checkout_id, buyer_id, shop_id, seller_user_id, total, items:[{sku_id, quantity, unit_price}]` |
| `OrderStatusChanged` | `order_id, from, to, actor_id, actor_role` |
| `OrderCancelled` | `order_id, reason, cancelled_by` (`buyer`\|`seller`\|`system`) |

Kafka headers carry `traceparent`. Matching Go structs live in `pkg/event`.

## 7. Shared libraries `pkg/`

| Package | Contents |
|---|---|
| `config` | load env into structs (`caarlos0/env`) |
| `logx` | JSON `slog`, automatically attaches `request_id`, `trace_id` from context |
| `httpx` | `WriteJSON`, `Decode` (+ `go-playground/validator`), `AppError{Code,Status,Message,Details}` + `WriteError`, middleware `RequestID`, `Recover`, `AccessLog` |
| `authctx` | middleware reading `X-User-Id/Roles` into context; `UserID(ctx)`, `HasRole(ctx, r)`, `RequireUser`, `RequireRole` |
| `pgxx` | pool creation, migrations from `embed.FS` (golang-migrate), `WithTx` helper |
| `outbox` | `Write(ctx, tx, Message)` + `Relay` (polls with `FOR UPDATE SKIP LOCKED`, batch 100, every 500 ms, publishes then sets `published_at`) |
| `kafkax` | producer/consumer (`franz-go`) with trace propagation, consumer groups, retry with backoff |
| `event` | envelope + the three event structs, encode/decode helpers |
| `grpcx` | server/client constructors with otel, default timeouts, health service |
| `otelx` | `Setup(ctx, serviceName)` → OTLP tracer provider; HTTP (`otelhttp`) & gRPC instrumentation |
| `testutil` | testcontainers helpers: `Postgres(t)`, `Redis(t)`, `Kafka(t)`, `MinIO(t)` |
| `server` | runs HTTP + gRPC with graceful shutdown on signals |

## 8. Service skeletons

Each `services/<svc>/` contains: `cmd/server/main.go` (config, logging, otel, `/healthz`, `/readyz`, graceful shutdown), an empty `internal/` following `domain/ app/ adapter/{http,grpc,postgres,...}`, empty `migrations/`, `sqlc.yaml`, a multi-stage distroless `Dockerfile`, and `go.mod`. After WS0 merges, **each directory is owned by its workstream**.

## 9. Local infrastructure & tooling

`deploy/docker-compose.yml`: `postgres:17` (init creates `identity_db`, `catalog_db`, `order_db`), `redis:7`, single-node KRaft Kafka (+ init job creating topic `order.events`), `minio` (+ init job creating public-read bucket `product-images`), `jaeger` all-in-one (UI :16686, OTLP :4317), the four services (profile `app`), and `web` (profile `app`). Only gateway, web, Jaeger UI and MinIO (API + console) publish ports to the host.

Makefile: `up` (infra), `up-app` (infra + apps), `down`, `gen` (buf + sqlc), `lint` (golangci-lint, buf lint, redocly lint), `test` (`go test ./...` across all modules), `keys` (generates a dev RSA key into `deploy/secrets/`, gitignored), `seed` (placeholder, completed by WS6), `e2e` (placeholder).

CI `.github/workflows/ci.yml`: lint + test jobs over a matrix of Go modules; `buf breaking` against `main`; web job (runs when `web/` exists).

## 10. Acceptance criteria

- [ ] `make gen` generates `gen/` cleanly; `buf lint` and `redocly lint api/openapi/*.yaml` pass.
- [ ] `make up-app` → all four skeleton services report healthy (`/readyz` 200).
- [ ] `pkg/` has tests for `httpx` (error mapping), `authctx`, `event` (encode/decode), and `outbox` (integration with Postgres + Kafka containers: a rolled-back tx publishes nothing; a committed tx publishes exactly once).
- [ ] `make lint && make test` green; CI green.
- [ ] `.gitignore` covers Go, Node, `.DS_Store`, `deploy/secrets/`, `.env`.

## 11. Decisions made during implementation

Gaps in §2–§9 resolved with the simplest option:

1. **Module wiring:** each service `go.mod` requires `pkg`/`gen` at `v0.0.0` with `replace` to `../../pkg` / `../../gen`, so modules also build with `GOWORK=off` (Docker, `go mod tidy`). `go.work.sum` is committed.
2. **Tools:** pinned in the `Makefile` and installed into `./bin` (`make tools`), not via `tool` directives, so tool dependencies never leak into service module graphs. golangci-lint v2; redocly via `npx @redocly/cli@<pinned>`.
3. **buf:** `buf.yaml` excludes lint rule `SERVICE_SUFFIX` because the contract fixes the names `IdentityInternal`/`CatalogInternal`. Code is generated with local plugins (`protoc-gen-go`, `protoc-gen-go-grpc`) into `gen/<svc>/v1`.
4. **Gateway** has no database: no `migrations/`, no `sqlc.yaml`, no gRPC port. Other services ship a no-op baseline migration `000001_init` so the migrate-on-startup path is exercised; `make gen` skips sqlc for services without query files.
5. **Error details:** `VALIDATION_ERROR` → `details.fields` maps JSON field paths (e.g. `skus[0].price`) to messages; `OUT_OF_STOCK` → `details.shortages[] {sku_id, requested, available}`. Request body limit 1 MB.
6. **OpenAPI:** `common.yaml` holds shared components; auth is `bearerAuth` (JWT) and `refreshCookie`; seller-only operations carry `x-required-role: seller`; JWKS is documented with `x-internal: true`. `PATCH /api/shops/me` accepts `{name?, description?}` (slug immutable). `ProductInput` = `ProductPatch` with `category_id, name, status, skus` required. `Product.min_price` is null when no SKU is active; `image_url` fields are null when a product has no image. Seller confirm/ship/deliver accept an optional `{reason}` note; cancel requires `reason` (1–200). Each service spec (`identity.yaml`/`catalog.yaml`/`order.yaml`) `$ref`s `bearerAuth`/`refreshCookie` from `common.yaml` under its own `components.securitySchemes`, because OpenAPI requires security schemes referenced by an operation to resolve within the same document.
7. **Events:** `OrderStatusChanged.actor_id` is omitted when `actor_role` is `system`. Payload JSON Schemas live in `api/events/*.schema.json` and are enforced by `pkg/event` tests.
8. **Outbox table:** `outbox(id uuid pk, topic, msg_key, payload jsonb, headers jsonb, created_at, published_at)` with a partial index on unpublished rows; SQL exported as `outbox.SchemaSQL` for services to copy into a migration. Delivery is at-least-once; the writer's `traceparent` is stored in `headers`.
9. **Contract test helper:** `pkg/testutil.ContractTransport` (kin-openapi) validates requests/responses in WS1–WS3 integration tests.
10. **Health:** `/healthz` and `/readyz` bypass middleware; containers are distroless, so the binary implements `server healthcheck` for the compose healthcheck.
11. **Images:** MinIO no longer publishes official Docker images; compose and tests use the community builds `pgsty/minio` / `pgsty/mc` (pinned releases). Kafka tests use `confluentinc/confluent-local` (testcontainers' supported image); compose uses `apache/kafka:4.3.1`. `pgsty/minio:RELEASE.2026-08-04T00-00-00Z` and `pgsty/mc:RELEASE.2026-09-16T00-00-00Z` were verified working on the first pull; `make up-app` (`docker compose ... --profile app up -d --build --wait`) succeeded with the `kafka-init`/`minio-init` one-shot containers reported as `Exited (0)`, so the workaround anticipated for `--wait` treating them as failures was not needed.
12. **Web in compose:** defined in `deploy/docker-compose.web.yml`, included by the Makefile only once `web/package.json` exists. WS5 must provide `web/Dockerfile` serving on :80 and proxying `/api` to `http://gateway:8080`; host port 3000.
13. **OpenAPI lint exceptions** (`api/redocly.yaml`, `api/.redocly.lint-ignore.yaml`): `info-license`/`info-license-strict` are off because this portfolio project has no published license; `no-server-example.com` is off as a deliberate choice, since every spec genuinely targets `http://localhost:8080` (the gateway port, per §3), not a placeholder domain. `no-unused-components` stays enabled; `common.yaml`'s shared parameters/schemas/responses/security schemes are suppressed by path in `.redocly.lint-ignore.yaml` because Redocly lints each spec file as its own document and would otherwise flag components that are only ever consumed via cross-file `$ref` from `identity.yaml`/`catalog.yaml`/`order.yaml`. Nine operations also needed an explicit `summary` to satisfy the `operation-summary` rule.
14. **Tooling on macOS:** the `Makefile` calls pinned tools by their explicit `bin/` path (`$(BUF)`, `$(SQLC)`, `$(GOLANGCI_LINT)`) rather than relying on `PATH` lookup, and sets `PATH` inline for `buf generate`'s plugin invocations, so the same `Makefile` works under macOS's bundled GNU Make 3.81 as well as newer `make` versions.
15. **Config errors:** `pkg/config.Load` uses `caarlos0/env`, which names the Go struct field (e.g. `PendingTimeout`) in parse-error messages and the environment variable name (e.g. `SERVICE_NAME`) in `required`/`notEmpty` error messages; `config.Load` wraps the error (`fmt.Errorf("load config: %w", ...)`) but does not rewrite or normalize the underlying message.
16. **Event envelope validation:** `event.Decode` rejects an envelope missing `occurred_at` (`OccurredAt.IsZero()`), in addition to the existing checks for `event_id`, `event_type`, and `data`.
17. **Known gaps left for owning workstreams:** MinIO and Jaeger have no compose `healthcheck:` (`minio-init` retries `mc alias set` until MinIO answers; nothing in the compose file gates a service's startup on MinIO's health, since it has none to gate on). Similarly, `pkg/otelx`'s own tests only confirm OTLP export doesn't hang when nothing is listening on `:4317` — actually exercising the OTLP-to-Jaeger export path end-to-end (a span appearing in the Jaeger UI) is left to WS6's trace test.
