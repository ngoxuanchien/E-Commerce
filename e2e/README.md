# e2e — integration tests and demo data

Everything here runs against the real Docker Compose stack (`make up-app`):
the gateway on `:8080`, the web app on `:3000`, Jaeger on `:16686`, MinIO on `:9000`.

| Path | What it is |
|---|---|
| `client/` | Small typed REST client for the gateway (cookie jar, bearer token, 429 back-off) |
| `cmd/seed/` | `make seed`: demo sellers, shops, products with images, buyers and orders, through public APIs only |
| `cmd/genimages/` | Regenerates the synthetic images in `fixtures/images/` |
| `api/` | Go API smoke tests; every request/response is validated against `api/openapi/*.yaml` |
| `playwright/` | Browser tests driving the real web app |

## Running

Prerequisites: a running stack (`make up-app`), Go 1.27.1, Node 24.

```bash
make seed      # idempotent; prints the demo accounts
make e2e       # Go tests (unit + API smoke) then Playwright
```

One test at a time:

```bash
cd e2e && GOWORK=off go test -count=1 -v -run TestCancelReturnsStock ./api/
cd e2e/playwright && npx playwright test tests/oversell.spec.ts
npx playwright show-trace test-results/<test>/trace.zip   # after a failure
```

`e2e` is its own Go module and is deliberately not part of `go.work` (owned
by the foundation workstream), so every Go command here runs with
`GOWORK=off`.

| Variable | Default | Used by |
|---|---|---|
| `GATEWAY_URL` | `http://localhost:8080` | seed, API tests, Playwright setup |
| `WEB_URL` | `http://localhost:3000` | Playwright, rate-limit proxy test |
| `JAEGER_URL` | `http://localhost:16686` | trace test |

## Why runs are slow right after `make seed`

The gateway allows 10 register/login calls per minute per client IP (burst
10, then one every 6 s). Seed and the tests all come from one IP, so clients
honor `Retry-After` and wait instead of failing. The gateway rejects before
proxying, so resending is always safe. A full `make e2e` takes a few minutes
mostly for this reason.

## Isolation

Every test creates its own users, shops and products, named with a random
8-letter run id, and clears its buyers' carts first. Tests never depend on
seed data or on earlier runs, so they can run against a stack that has
already been used.

## Findings

Integration found three bugs on the real stack, each first pinned by a failing
test and then fixed. The tests now run as ordinary regression tests:

| Id | Bug | Fixed in | Regression test |
|---|---|---|---|
| F1 | All browser traffic shared one gateway rate-limit bucket (`TRUSTED_PROXIES` empty) | `deploy/`: the gateway trusts only the web container's fixed address | `api/ratelimit_test.go` |
| F2 | Public operations did not document the 401 for an invalid token | `api/openapi/`: 401 added, web types regenerated | `api/auth_test.go` (contract-checked) |
| F3 | A lost checkout response replaced the checkout form with an empty state | `web/src/api/hooks/orders.ts`: no cart refetch after retryable errors | `playwright/tests/idempotency.spec.ts`, `web` unit test |

## P1 success criteria → tests

| # | Criterion | Automated by |
|---|---|---|
| 1 | Register/login, browse, search with/without diacritics, details, cart from several shops | `api/flow_test.go`, `playwright/tests/happy-path.spec.ts`, `playwright/tests/auth.spec.ts` |
| 2 | Checkout splits the cart into one COD order per shop | `api/flow_test.go`, `happy-path.spec.ts` |
| 3 | Open a shop → seller role → products with SKUs and images | `happy-path.spec.ts` (UI upload, two variants), `api/flow_test.go` (presigned upload) |
| 4 | Confirm → ship → deliver → complete; cancelling returns stock | `api/flow_test.go`, `happy-path.spec.ts`, `api/cancel_test.go`, `cancel-restock.spec.ts` |
| 5 | Stock never oversells under concurrent orders | `api/oversell_test.go`, `oversell.spec.ts`, `api/idempotency_test.go`, `idempotency.spec.ts` |
| 6 | Automated tests pass in every service; a checkout is traceable gateway → order → catalog | CI `go` matrix and `web` job; `api/trace_test.go` |
