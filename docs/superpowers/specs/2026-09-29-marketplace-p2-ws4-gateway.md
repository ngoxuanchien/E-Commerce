# WS4': gateway changes for P2

> Depends on WS0' (`payment.yaml`, `ledger.yaml`, and the `PAYMENT_URL`/`LEDGER_URL` compose variables). Owns `services/gateway/`. During development the upstreams are httptest servers. This spec amends P1 `2026-09-28-marketplace-p1-ws4-gateway.md`. The pipeline, middleware order and P1 rulings (WS4 follow-up notes: canonical-path 404, method allow-list, 307 redirects) are unchanged.

## 1. Routing additions

| Prefix | Upstream | Auth level |
|---|---|---|
| `/api/payments/vnpay/`, `/api/payments/stripe/`, `/api/payments/fake/` | payment `:8084` | **Public** (provider callbacks, return URLs, FAKE hosted page) |
| `/api/payments` (everything else, including `/api/payments/{id}`) | payment `:8084` | User |
| `/api/seller/wallet`, `/api/seller/payouts` | ledger `:8085` | Seller (already covered by the P1 `/api/seller/*` rule) |

- **Longest prefix wins.** The public provider prefixes must be matched before the `/api/payments` User route. The route-table test pins this.
- `/api/seller/wallet` and `/api/seller/payouts` must not fall through to catalog or order. Today's `/api/seller/products|uploads|orders` prefixes don't overlap, and a test pins that too.
- New config: `PAYMENT_URL`, `LEDGER_URL`, each validated at startup like the P1 upstream URLs.

## 2. Pass-through requirements for provider callbacks

These are behaviours the P1 proxy already has. P2 adds tests that pin them, because signature verification downstream depends on them:
1. **The raw body is forwarded byte for byte**, with no re-encoding or decompression. Stripe signs the raw body.
2. **The raw query string is forwarded unchanged** (`URL.RawQuery`), including its order and percent-encoding. VNPay signs the parameters.
3. The `Stripe-Signature` and `X-Fake-Signature` request headers are forwarded. `302 Location` and `Content-Type: text/html` responses are passed through untouched.
4. **An `Authorization` header on a public callback route is ignored if invalid.** This is a deliberate exception to P1's "invalid token → 401 even on public routes", scoped to the three provider prefixes only. A provider never sends a token, and a browser coming back from a provider might carry a stale one on the return URL. Identity headers are still stripped, and nothing is injected.
5. **The access log records the route prefix and never the query string** (P1 rule). VNPay query strings contain transaction data and must not be logged.
6. The 1 MB body limit applies. Stripe events are far smaller.
7. The rate limit uses the P1 per-IP bucket (20 req/s, burst 40). Provider callbacks and the FAKE webhook (which comes from the payment container, through the gateway) stay well under it. No new bucket is added.

## 3. Testing

- Route matching for every row in §1, including longest-prefix and non-overlap with catalog and order seller routes.
- Auth levels:
  - an anonymous request to `/api/payments/stripe/webhook` is proxied;
  - an anonymous `POST /api/payments` → 401;
  - a buyer token on `/api/seller/wallet` → 403, and a seller token → proxied to ledger;
  - an invalid bearer token on `/api/payments/vnpay/return` → proxied without identity headers, while the same token on `/api/payments/{id}` → 401.
- Pass-through with an echo upstream:
  - a body whose bytes include `\r\n` and non-ASCII characters arrives with an identical SHA-256;
  - a query `?vnp_B=2&vnp_A=%C3%A1+x` arrives with an identical `RawQuery`;
  - the signature headers arrive;
  - `302 Location` is returned unchanged.
- The access log for a VNPay return contains no `vnp_` text.

## 4. Acceptance criteria

- [ ] Every route in `payment.yaml` and `ledger.yaml` is reachable with the right auth level through the gateway.
- [ ] Callback bodies, query strings and signature headers reach payment-svc unchanged, and the tests prove it.
- [ ] The P1 gateway tests still pass. `go test ./...` and lint are green.
