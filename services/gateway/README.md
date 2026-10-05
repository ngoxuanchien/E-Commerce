# gateway

Single public entry point for the marketplace: JWT auth (RS256 via JWKS), HTTP routing to five backend services, CORS, and per-IP rate limiting.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `IDENTITY_URL` | — (required) | identity-svc base URL |
| `CATALOG_URL` | — (required) | catalog-svc base URL |
| `ORDER_URL` | — (required) | order-svc base URL |
| `PAYMENT_URL` | — (required) | payment-svc base URL |
| `LEDGER_URL` | — (required) | ledger-svc base URL |
| `IDENTITY_JWKS_URL` | — (required) | identity-svc JWKS endpoint (RS256 keys) |
| `CORS_ALLOWED_ORIGINS` | — | Comma-separated origins allowed in CORS `Access-Control-Allow-Origin` |
| `UPSTREAM_TIMEOUT` | `10s` | Dial and response-header timeout for every upstream service |
| `TRUSTED_PROXIES` | — | Comma-separated CIDRs or IPs (e.g. `10.0.0.0/8,127.0.0.1`) whose `X-Forwarded-For` is honored for client IP; empty means trust no proxies |
| `HTTP_ADDR` | `:8080` | HTTP server address |
| `LOG_LEVEL` | `info` | Log level |
| `SERVICE_NAME` | — (required) | Service name for logs and tracing |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | OpenTelemetry collector endpoint (if not set, tracing is disabled) |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown timeout |

## Request pipeline

Every request flows through:

1. **RequestID** — adds a correlation ID to logs and response headers
2. **Tracing** — OpenTelemetry span per request
3. **Access log** — method, matched route pattern, status, duration, user ID (if authenticated)
4. **Recovery** — catches panics and returns 500
5. **CORS** — answers OPTIONS preflights with 204 for allowed origins and 403 for disallowed; sets `Access-Control-Allow-Origin` on actual responses from allowed origins
6. **Rate limit** — per-IP token buckets; login routes: 10/min burst 10; all others: 20/s burst 40; denied: 429 with `Retry-After`
7. **Body limit** — rejects bodies > 1 MiB (`httpx.MaxBodyBytes`); declared `Content-Length` fails immediately, streaming bodies return 413
8. **Dispatcher** — checks method and path canonicality (404 if not), matches route table, runs auth gate, proxies to upstream

The static route table (`internal/routes/routes.go`) lists method, path pattern, auth level, and upstream per route; first match wins. Routes may be leaf (exact path) or subtree (match prefix + deeper paths).

## Design decisions

**Auth levels:**

- `Public` — no authentication required
- `User` — valid access token for any user (roles don't matter)
- `Seller` — valid access token with "seller" role
- `Callback` — provider callbacks (P2); no token verification, invalid Authorization headers ignored, identity never injected

**P1 rulings:**

- **Unclean paths and redirects:** Go's `ServeMux` answers unclean paths (`..`, `//`, `/./`) with a 307 redirect for most methods, and that redirect bypasses the gateway pipeline (no request ID, access log, rate limit, or body limit). The dispatcher catches non-canonical paths with a 404 NOT_FOUND response before route matching, so a client cannot slip an unclean path through to the upstream.
- **Disallowed methods:** the dispatcher rejects methods outside GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS with 404 NOT_FOUND. This catches CONNECT and other methods that `ServeMux` does not redirect.
- **Trust boundary:** gateway strips all incoming `X-User-Id` and `X-User-Roles` headers (case-insensitive, including `_` variants) before processing; only the gateway's own verified identity is trusted by upstreams.
- **Rate limit:** shared bucket across all routes within its category (login vs general); client IP is the requesting peer unless `TRUSTED_PROXIES` is set and the peer is trusted, in which case the rightmost non-trusted hop of `X-Forwarded-For` is used.
- **`/api/shops/me` auth:** Seller-only (routes to catalog-svc), precedes the public `GET /api/shops/{slug}` in the table to prevent "me" being treated as a slug.
- **Access log:** records the matched route pattern only, never the raw path or query string (important for callbacks, which may carry sensitive VNPay transaction data in the query).

**P2 additions:**

- **Callback routes** skip token verification and never inject identity headers. A provider webhook has no token, and a browser returning from the provider may carry a stale or invalid Authorization header that must be ignored rather than rejected. The three callback subtrees in the route table — `/api/payments/vnpay`, `/api/payments/stripe`, `/api/payments/fake` — precede the general `/api/payments` User route (first match wins). Payment IDs are UUIDs, so there is no clash with provider names.
- **Rate limit bucket:** callbacks share the general 20/s bucket (spec §2.7, chosen over a separate bucket on 2026-10-05).
