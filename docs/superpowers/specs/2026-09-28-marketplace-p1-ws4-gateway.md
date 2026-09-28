# WS4 — gateway

> Depends on: WS0 (route/auth table in WS0 §4, `pkg/`). Owns: `services/gateway/`. During development upstreams are `httptest` servers and JWKS is served by a stub with a test key pair.

## 1. Responsibility

The single public entry point: routing, authentication (JWT verification), coarse authorization (login / seller role), identity header injection, CORS, rate limiting, request IDs, tracing. **No business logic** and no database.

## 2. Routing

| Prefix | Upstream |
|---|---|
| `/api/auth/`, `/api/users/` | identity `:8081` |
| `/api/categories`, `/api/products`, `/api/shops`, `/api/seller/products`, `/api/seller/uploads` | catalog `:8082` |
| `/api/cart`, `/api/checkout`, `/api/orders`, `/api/seller/orders` | order `:8083` |
| anything else | 404 `NOT_FOUND` |

The route table is data (a Go slice built from config: prefix, upstream, auth level, optional method set) so it's unit-testable. Proxying uses `httputil.ReverseProxy` with pooled transports; the path is forwarded unchanged.

## 3. Authentication & authorization

Auth levels (from WS0 §4):
- **Public:** `POST /api/auth/{register,login,refresh,logout}`, `GET /api/categories`, `GET /api/products*`, `GET /api/shops/{slug}`.
- **User:** every other route.
- **Seller:** every `/api/seller/*` route and `GET|PATCH /api/shops/me`.

Pipeline per request:
1. **Always strip** incoming `X-User-Id` / `X-User-Roles` (clients cannot spoof them).
2. If `Authorization: Bearer <jwt>` is present: verify RS256 signature via JWKS, `iss=identity`, `exp`/`nbf` with 30 s leeway. Invalid token → 401 even on public routes (fail loudly). Valid → set `X-User-Id=sub`, `X-User-Roles=roles`.
3. Missing token on a User/Seller route → 401 `UNAUTHENTICATED`; missing `seller` role on a Seller route → 403 `FORBIDDEN`.

Upstream services still enforce ownership (the gateway only does coarse checks).

**JWKS client:** fetch `IDENTITY_JWKS_URL` at startup (retry with backoff; `/readyz` is 503 until loaded), cache keys, refresh every 5 min, and refresh on unknown `kid` (at most once per 30 s to prevent abuse).

## 4. Cross-cutting middleware (in order)

1. `RequestID` — reuse a valid incoming `X-Request-Id` or generate one; echo it in the response.
2. OpenTelemetry server span; propagate `traceparent` upstream.
3. Access log (method, route prefix, status, latency, user id; never tokens or bodies).
4. Recover → 500 `INTERNAL`.
5. CORS — origins from `CORS_ALLOWED_ORIGINS`, credentials allowed (refresh cookie), preflight handled at the gateway.
6. Rate limit — in-memory token bucket per client IP: `/api/auth/login` and `/api/auth/register` 10 req/min; everything else 20 req/s, burst 40. Exceeded → 429 `RATE_LIMITED` with `Retry-After`.
7. Body limit — 1 MB (`413` with standard error body).
8. Auth (§3) → proxy.

Upstream errors: connection refused / timeout (`UPSTREAM_TIMEOUT=10s`) → 503 `UPSTREAM_UNAVAILABLE` in the standard error format. Upstream responses are passed through untouched.

## 5. Code layout

```
internal/routes     route table + matcher
internal/auth       JWKS cache, JWT verifier, auth middleware
internal/ratelimit  per-IP token buckets with idle eviction
internal/proxy      reverse proxy + error handler
cmd/server          wiring
```
Service-specific env: `IDENTITY_URL`, `CATALOG_URL`, `ORDER_URL`, `IDENTITY_JWKS_URL`, `CORS_ALLOWED_ORIGINS`, `UPSTREAM_TIMEOUT`, `TRUSTED_PROXIES` (for client IP resolution; empty = use remote addr).

## 6. Testing

- Unit: route matching (every row in §2 and §3's auth levels); rate limiter; JWKS refresh on unknown `kid` + throttle.
- Middleware tests with `httptest` upstreams echoing received headers:
  - spoofed `X-User-Id` is stripped;
  - valid token → headers injected; expired / wrong issuer / wrong key → 401;
  - buyer token on `/api/seller/products` → 403; seller token → proxied;
  - public route without token → proxied without identity headers;
  - upstream down → 503 with standard error body; `X-Request-Id` echoed; `traceparent` forwarded.
- CORS preflight from an allowed and a disallowed origin.

## 7. Acceptance criteria

- [ ] Every route in WS0 §4 is reachable with the correct auth level.
- [ ] Clients can never inject `X-User-*` headers.
- [ ] `/readyz` is 200 only after JWKS is loaded.
- [ ] `go test ./...` + lint green.
