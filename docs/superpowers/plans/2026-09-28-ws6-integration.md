# WS6 Integration & E2E Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove the six P1 success criteria on the real Docker Compose stack (seed data, Go API smoke tests, Playwright browser tests, a Jaeger trace check). Document the system for portfolio readers and add an `e2e` CI job.

**Architecture:** One new standalone Go module, `e2e/`. It is not listed in `go.work`, which WS0 owns, so every Go command runs with `GOWORK=off`. It contains:
- a small typed REST client for the gateway (`e2e/client`), which also handles the gateway's 429s;
- the seed program (`e2e/cmd/seed`), which talks only to public APIs through the gateway;
- a fixture-image generator (`e2e/cmd/genimages`);
- the API smoke tests (`e2e/api`), which validate every request and response against `api/openapi/*.yaml` with `pkg/testutil.ContractTransport`.

A separate npm project, `e2e/playwright/`, drives the real web app at `http://localhost:3000`. Tests prepare data through the gateway at `http://localhost:8080` and drive the browser only for the behavior under test. Every test creates its own users, shops and products, named with a random run id, so tests never depend on seed data or on earlier runs.

**Tech Stack:** Go 1.27.1, `net/http` + `net/http/cookiejar`, testify, `pkg/testutil` (kin-openapi). Playwright `@playwright/test` ^1.63 with TypeScript ~5.9, Node 24. The Jaeger v2 query HTTP API (`/api/traces/{id}`). GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-28-marketplace-p1-ws6-integration.md`. Also read `...-overview.md` §1 (the six success criteria) and `...-ws0-foundation.md` §4 (REST contract). All specs are read-only.

## Global Constraints

- Only modify: `e2e/`, `docs/` except `docs/superpowers/specs/`, `README.md`, the `seed` and `e2e` targets in `Makefile`, the new `e2e` job in `.github/workflows/ci.yml`, and this plan file. Never edit `services/`, `web/`, `api/`, `proto/`, `gen/`, `pkg/`, `deploy/`, `go.work`, `go.work.sum` or `.gitignore`.
- **Never patch service or web code.** When the real stack shows a bug: (1) reproduce it with a failing e2e/API test; (2) find the root cause with superpowers:systematic-debugging; (3) record it in the plan's "Findings" section with the owning workstream, the evidence and the suggested fix; (4) report it to the user. A test that pins an unfixed bug is skipped unless `E2E_FINDINGS=1` is set, so the suite stays green. The skip message names the finding id.
- The seed program only uses public REST APIs through the gateway (`GATEWAY_URL`, default `http://localhost:8080`), plus the presigned-POST upload URL that the API returns.
- Module path `github.com/ngoxuanchien/e-commerce/e2e`, `go 1.27.1`. It requires `github.com/ngoxuanchien/e-commerce/pkg v0.0.0` with `replace => ../pkg`. Every Go command in `e2e/` runs with `GOWORK=off`.
- Synthetic accounts use the domain `@example.test`. Every demo and test password is `matkhau123`.
- Money is `int64` VND. The shipping fee is 30,000 per order (`SHIPPING_FEE_FLAT`).
- Gateway rate limits per client IP: `/api/auth/login` and `/api/auth/register` allow a burst of 10, then 1 request per 6 s; everything else allows 20 req/s with a burst of 40. The 429 response carries `Retry-After`. The gateway rejects before proxying, so resending after `Retry-After` is safe for every method. All clients (Go and Playwright) retry 429s this way and never hammer the gateway.
- Playwright runs with `workers: 1`, because the whole suite shares one stack and one rate-limit bucket.
- The stack is started by `make up-app` (compose project `marketplace`). Its Postgres and MinIO volumes are shared with the other worktrees. **Never run `docker compose down -v` or restart the shared stack without asking the user first.**
- UI copy asserted in Playwright must be copied from `web/src` (Vietnamese). Don't invent labels; grep for them.
- All code, comments, docs and commit messages are in English. Vietnamese appears only in demo data and in asserted UI copy.
- After each task passes review, tick its steps `- [x]` in this file and commit (`docs: mark WS6 Task N done`).

## Review Focus

1. **Rate-limit exhaustion from one host IP.** `make seed` immediately followed by `make e2e` exhausts the login bucket. Expected: clients wait out `Retry-After` and the run stays green, only slower. Pinned in Task 1 (`TestRetryRateLimitedWaitsAndResendsBody`, `TestRetryRateLimitedGivesUpAfterMaxWait`). Task 2 Step 6 runs seed twice back to back.
2. **Seed re-run after a partial first run.** Cases: the user exists but has no shop; the shop exists but some products are missing; orders already exist. Expected: a re-run resumes, creates only what's missing and never duplicates. Task 2 Step 6 pins this by running seed twice and comparing product and order counts through the API.
3. **Leftover data from earlier runs and from seed** (same product names, stale carts). Expected: tests only assert on run-unique names and clear their buyers' carts first. Pinned by `runID()` / `runId()` and the `ClearCart` calls in Tasks 3–7.
4. **Async stock release lag.** Kafka consumer join after a stack restart delays the `OrderCancelled` handling. Expected: stock is back within 5 s (spec). Pinned in Task 4 (`TestCancelReturnsStock`) and Task 7 (`cancel-restock.spec.ts`). If it's slower, that's a finding, not a reason to raise the timeout.
5. **Late trace export.** The gateway, order and catalog spans reach Jaeger at different times (batch exporters). Expected: the test polls for up to 30 s until all three services and a `ReserveStock` span are present. Pinned in Task 5 (`TestCheckoutTraceCrossesGatewayOrderCatalog`).

## Findings (filled in during execution)

Candidate to confirm in Task 3: **F1 — browser traffic shares one rate-limit bucket.** `deploy/docker-compose.yml` sets the gateway's `TRUSTED_PROXIES: ""`. Every request from the web container's nginx therefore gets the nginx container IP as its client IP. All browser users share one login bucket: 10 logins per minute, site-wide. Owner: WS0 (`deploy/`). Suggested fix: set `TRUSTED_PROXIES` to the compose network CIDR, or to the `web` service's address. Reproduction: `e2e/api/findings_test.go` `TestBrowserClientsHaveSeparateLoginBuckets` (Task 3 Step 6).

| Id | Owner | Symptom | Root cause | Evidence | Suggested fix | Status |
|---|---|---|---|---|---|---|
| F1 | WS0 (`deploy/docker-compose.yml`) | Every browser user shares one gateway rate-limit bucket: site-wide 10 logins/min and 20 req/s for all traffic through the web app | `TRUSTED_PROXIES: ""` on the gateway, so `ratelimit.ClientIP.Resolve` (`services/gateway/internal/ratelimit/clientip.go`) ignores the `X-Forwarded-For` that nginx (`web/nginx.conf`, `$proxy_add_x_forwarded_for`) appends and keys every request on the peer = web container IP (172.18.0.11) | `E2E_FINDINGS=1 go test -run TestBrowserClientsHaveSeparateLoginBuckets ./api/` → 11 logins from 11 distinct X-Forwarded-For clients via :3000, 11th answered 429 (`Should be zero, but was 1`) | Set the gateway's `TRUSTED_PROXIES` to the compose network (e.g. give the network a fixed subnet and trust it, or trust the `web` service address) | reported |
| F2 | WS0 (`api/openapi/*.yaml`) | Public operations (`GET /api/categories`, `GET /api/products`, `GET /api/products/{id}`, `GET /api/shops/{slug}`, `POST /api/auth/{register,login}`) do not document the 401 that the gateway returns for an invalid bearer token (WS4 §3 "fail loudly"), so contract validation rejects a correct response and generated web types don't model it | Contract omission: those operations list only 200/4xx business codes + 429 | `E2E_FINDINGS=1 go test -run TestPublicRoutesDocumentInvalidToken401 ./api/` → `response violates contract: GET /api/products -> 401: status is not supported` | Add `'401': $ref: 'common.yaml#/components/responses/Unauthenticated'` (or the existing unauthenticated response) to every public operation | reported |

---

## File Structure

```
e2e/
  go.mod, go.sum                  module github.com/ngoxuanchien/e-commerce/e2e (GOWORK=off)
  README.md                       how to run seed / API tests / Playwright, env vars
  client/
    client.go                     Client (base URL, cookie jar, bearer token), Do(), APIError, IsCode/StatusOf
    retry.go                      RetryRateLimited RoundTripper (honors Retry-After)
    types.go                      DTOs for the subset of the REST contract e2e uses
    endpoints.go                  typed endpoint methods (auth, catalog, cart, checkout, orders)
    upload.go                     UploadFile: multipart POST to the presigned URL
    client_test.go, retry_test.go unit tests (httptest)
  cmd/genimages/main.go           writes the deterministic JPEG fixtures
  cmd/seed/
    main.go                       flags/env, signal handling, exit code
    data.go                       demo dataset: 3 sellers x 7-8 products, 2 buyers, 5 order plans
    seed.go                       idempotent seeding logic
    data_test.go                  dataset sanity (counts, limits, fixture files exist)
  fixtures/images/*.jpg           22 generated product images (+ README.md)
  api/
    main_test.go                  TestMain: wait for gateway /readyz
    harness_test.go               contract transport, actors, shared world, helpers
    flow_test.go                  search, two-shop checkout, lifecycle, image upload
    auth_test.go                  401/403, header spoofing, refresh rotation, logout
    cancel_test.go                cancel returns stock (buyer + seller)
    oversell_test.go              concurrent checkouts never oversell
    idempotency_test.go           same key replays, reused key 422, concurrent double submit
    trace_test.go                 Jaeger: one trace with gateway + order + catalog ReserveStock
    findings_test.go              reproductions of reported bugs (skipped unless E2E_FINDINGS=1)
  playwright/
    package.json, package-lock.json, tsconfig.json, playwright.config.ts
    support/api.ts                ApiUser (gateway setup calls with 429 retry), runId
    support/ui.ts                 signIn, registerViaUi, loginViaUi, logout, search, addToCart, openCheckout
    tests/auth.spec.ts            session survives reload; logout blocks /orders; buyer can't open seller center
    tests/happy-path.spec.ts      full P1 journey across two shops
    tests/cancel-restock.spec.ts  cancel returns stock within 5 s
    tests/oversell.spec.ts        two browser contexts race for the last unit
    tests/idempotency.spec.ts     double click + lost response retry create one set of orders
docs/
  architecture.md                 services, ownership, checkout/cancel sequence diagrams, consistency
  adr/0001-...md … 0005-...md     five short ADRs
README.md                         project front page
Makefile                          seed + e2e targets only
.github/workflows/ci.yml          new e2e job only
```

---

### Task 1: e2e Go module, gateway client and fixture images

**Files:**
- Create: `e2e/go.mod`, `e2e/client/{client.go,retry.go,types.go,endpoints.go,upload.go}`, `e2e/client/{client_test.go,retry_test.go}`, `e2e/cmd/genimages/main.go`, `e2e/fixtures/images/README.md`, `e2e/fixtures/images/*.jpg` (generated)

**Interfaces:**
- Produces (used by Tasks 2–5):
  - `client.New(baseURL string, rt http.RoundTripper) *Client`. A nil `rt` means `http.DefaultTransport`. It has a cookie jar and a 30 s timeout.
  - `(*Client).Do(ctx, method, path string, body, out any, hdr http.Header) error`, `(*Client).Token() string`, `(*Client).SetToken(string)`, and the field `(*Client).HTTP *http.Client`.
  - `client.APIError{Status int; Code, Message string; Details json.RawMessage}`, plus `client.IsCode(err, code) bool` and `client.StatusOf(err) int` (0 for non-API errors).
  - `client.RetryRateLimited{Base http.RoundTripper; MaxWait time.Duration}` (an `http.RoundTripper`).
  - Endpoint methods: `Register`, `Login`, `Refresh`, `Logout`, `Me`, `Categories`, `SearchProducts`, `Product`, `OpenShop`, `MyShop`, `SellerProducts`, `AllSellerProducts`, `CreateProduct`, `RequestUpload`, `Cart`, `SetCartItem`, `RemoveCartItem`, `ClearCart`, `Checkout`, `Orders`, `Order`, `CancelOrder`, `CompleteOrder`, `SellerOrders`, `SellerOrderAction`.
  - `client.UploadFile(ctx, hc *http.Client, up Upload, path, contentType string) error`.
  - `client.DemoAddress(fullName string) Address`.
  - Types: `User`, `TokenResponse`, `ShopRef`, `Shop`, `Category`, `Sku`, `Image`, `Product`, `ProductSummary`, `Page[T]`, `SkuInput`, `ProductInput`, `Upload`, `CartItem`, `CartShop`, `Cart`, `Address`, `CheckoutRequest`, `CheckoutResponse`, `OrderItem`, `HistoryEntry`, `OrderShop`, `Order`.

- [x] **Step 1: Create the module**

```bash
cd /Users/xuanchien/Project/claude-app/E-Commerce-ws6-integration/e2e
cat > go.mod <<'EOF'
module github.com/ngoxuanchien/e-commerce/e2e

go 1.27.1

require github.com/ngoxuanchien/e-commerce/pkg v0.0.0

replace github.com/ngoxuanchien/e-commerce/pkg => ../pkg
EOF
```

- [x] **Step 2: Write the failing client tests**

`e2e/client/retry_test.go`:

```go
package client_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

func TestRetryRateLimitedWaitsAndResendsBody(t *testing.T) {
	var calls atomic.Int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hc := &http.Client{Transport: client.RetryRateLimited{MaxWait: 5 * time.Second}}
	start := time.Now()
	resp, err := hc.Post(srv.URL, "application/json", strings.NewReader(`{"a":1}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(2), calls.Load())
	assert.Equal(t, []string{`{"a":1}`, `{"a":1}`}, bodies)
	assert.GreaterOrEqual(t, time.Since(start), time.Second)
}

func TestRetryRateLimitedGivesUpAfterMaxWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	hc := &http.Client{Transport: client.RetryRateLimited{MaxWait: time.Second}}
	start := time.Now()
	resp, err := hc.Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Less(t, time.Since(start), time.Second)
}
```

`e2e/client/client_test.go`:

```go
package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

func TestDoDecodesErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"OUT_OF_STOCK","message":"not enough stock","details":{"shortages":[]}}}`))
	}))
	defer srv.Close()

	err := client.New(srv.URL, nil).Do(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	require.Error(t, err)
	assert.True(t, client.IsCode(err, "OUT_OF_STOCK"))
	assert.Equal(t, http.StatusConflict, client.StatusOf(err))
	assert.JSONEq(t, `{"shortages":[]}`, string(err.(*client.APIError).Details))
}

func TestDoSendsBearerTokenAndHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"id":"u1"}`))
	}))
	defer srv.Close()

	c := client.New(srv.URL, nil)
	c.SetToken("tok")
	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, c.Do(context.Background(), http.MethodPost, "/y", map[string]int{"a": 1}, &out, http.Header{"Idempotency-Key": {"k1"}}))

	assert.Equal(t, "u1", out.ID)
	assert.Equal(t, "Bearer tok", got.Get("Authorization"))
	assert.Equal(t, "application/json", got.Get("Content-Type"))
	assert.Equal(t, "k1", got.Get("Idempotency-Key"))
}
```

- [x] **Step 3: Run the tests to verify they fail**

Run: `cd e2e && GOWORK=off go test ./client/...`
Expected: FAIL to compile (package `client` has no non-test files / undefined `client.RetryRateLimited`).

- [x] **Step 4: Implement the client**

`e2e/client/client.go`:

```go
// Package client is a small typed REST client for the marketplace gateway,
// used by the seed program and the API smoke tests.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// Client calls the gateway as one user: it keeps that user's refresh cookie
// in its jar and sends the current access token as a bearer token.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	token   string
}

// New returns a Client for baseURL. rt is the transport (nil means
// http.DefaultTransport); wrap it in RetryRateLimited to survive 429s.
func New(baseURL string, rt http.RoundTripper) *Client {
	jar, _ := cookiejar.New(nil) // cookiejar.New never fails with nil options
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Transport: rt, Jar: jar, Timeout: 30 * time.Second},
	}
}

// Token returns the current access token ("" before login).
func (c *Client) Token() string { return c.token }

// SetToken replaces the access token sent with every request.
func (c *Client) SetToken(t string) { c.token = t }

// APIError is a non-2xx gateway response in the standard error format.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details json.RawMessage
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message)
}

// IsCode reports whether err is an APIError with the given error code.
func IsCode(err error, code string) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == code
}

// StatusOf returns the HTTP status of an APIError, or 0 for other errors.
func StatusOf(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

// Do sends a JSON request and decodes a JSON response into out (if non-nil).
// Non-2xx responses become *APIError.
func (c *Client) Do(ctx context.Context, method, path string, body, out any, hdr http.Header) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: read body: %w", method, path, err)
	}

	if resp.StatusCode >= 300 {
		var env struct {
			Error struct {
				Code    string          `json:"code"`
				Message string          `json:"message"`
				Details json.RawMessage `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &env) // a non-JSON body still yields an APIError with the status
		return &APIError{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message, Details: env.Error.Details}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decode: %w", method, path, err)
		}
	}
	return nil
}
```

`e2e/client/retry.go`:

```go
package client

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// RetryRateLimited resends requests the gateway answered with 429, after the
// Retry-After delay. The gateway rate-limits before proxying, so the
// upstream never saw the rejected request and resending is safe for every
// method. A 429 is returned as-is once waiting would exceed MaxWait in total.
type RetryRateLimited struct {
	Base    http.RoundTripper
	MaxWait time.Duration
}

func (t RetryRateLimited) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	deadline := time.Now().Add(t.MaxWait)
	for {
		resp, err := base.RoundTrip(req)
		if err != nil || resp.StatusCode != http.StatusTooManyRequests {
			return resp, err
		}
		wait := retryAfter(resp.Header.Get("Retry-After"))
		if time.Now().Add(wait).After(deadline) {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(wait):
		}

		if req.Body != nil && req.Body != http.NoBody {
			if req.GetBody == nil {
				return nil, fmt.Errorf("retry %s %s: request body cannot be replayed", req.Method, req.URL.Path)
			}
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req = req.Clone(req.Context())
			req.Body = body
		}
	}
}

func retryAfter(v string) time.Duration {
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return time.Second
}
```

`e2e/client/types.go`:

```go
package client

// DTOs for the subset of api/openapi/*.yaml that seed and e2e use.

type User struct {
	ID       string   `json:"id"`
	Email    string   `json:"email"`
	FullName string   `json:"full_name"`
	Roles    []string `json:"roles"`
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	User        User   `json:"user"`
}

type ShopRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type Shop struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	OwnerUserID string `json:"owner_user_id"`
}

type Category struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Slug     string     `json:"slug"`
	Children []Category `json:"children"`
}

type Sku struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Price  int64  `json:"price"`
	Stock  int    `json:"stock"`
	Active bool   `json:"active"`
}

type Image struct {
	ObjectKey string `json:"object_key"`
	URL       string `json:"url"`
}

type Product struct {
	ID          string  `json:"id"`
	Shop        ShopRef `json:"shop"`
	CategoryID  string  `json:"category_id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	Images      []Image `json:"images"`
	Skus        []Sku   `json:"skus"`
	MinPrice    *int64  `json:"min_price"`
}

type ProductSummary struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	ImageURL *string `json:"image_url"`
	MinPrice *int64  `json:"min_price"`
	Shop     ShopRef `json:"shop"`
}

type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type SkuInput struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Price  int64  `json:"price"`
	Stock  int    `json:"stock"`
	Active bool   `json:"active"`
}

type ProductInput struct {
	CategoryID  string     `json:"category_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	ImageKeys   []string   `json:"image_keys"`
	Skus        []SkuInput `json:"skus"`
}

type Upload struct {
	UploadURL string            `json:"upload_url"`
	Fields    map[string]string `json:"fields"`
	ObjectKey string            `json:"object_key"`
	PublicURL string            `json:"public_url"`
}

type CartItem struct {
	SkuID       string `json:"sku_id"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	VariantName string `json:"variant_name"`
	UnitPrice   int64  `json:"unit_price"`
	Quantity    int    `json:"quantity"`
	Stock       int    `json:"stock"`
	Available   bool   `json:"available"`
}

type CartShop struct {
	Shop  ShopRef    `json:"shop"`
	Items []CartItem `json:"items"`
}

type Cart struct {
	Shops    []CartShop `json:"shops"`
	Subtotal int64      `json:"subtotal"`
}

type Address struct {
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
	Line1    string `json:"line1"`
	Ward     string `json:"ward"`
	District string `json:"district"`
	Province string `json:"province"`
}

// DemoAddress is a valid Vietnamese shipping address for fullName.
func DemoAddress(fullName string) Address {
	return Address{FullName: fullName, Phone: "0901234567", Line1: "12 Lý Tự Trọng", Ward: "Bến Nghé", District: "Quận 1", Province: "TP. Hồ Chí Minh"}
}

type CheckoutRequest struct {
	SkuIDs          []string `json:"sku_ids,omitempty"`
	ShippingAddress Address  `json:"shipping_address"`
	PaymentMethod   string   `json:"payment_method"`
}

type CheckoutResponse struct {
	CheckoutID string  `json:"checkout_id"`
	Orders     []Order `json:"orders"`
}

type OrderItem struct {
	SkuID       string `json:"sku_id"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	VariantName string `json:"variant_name"`
	UnitPrice   int64  `json:"unit_price"`
	Quantity    int    `json:"quantity"`
	LineTotal   int64  `json:"line_total"`
}

type HistoryEntry struct {
	From      *string `json:"from"`
	To        string  `json:"to"`
	ActorRole string  `json:"actor_role"`
	Note      *string `json:"note"`
}

type OrderShop struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Order struct {
	ID           string         `json:"id"`
	CheckoutID   string         `json:"checkout_id"`
	Status       string         `json:"status"`
	BuyerID      string         `json:"buyer_id"`
	Shop         OrderShop      `json:"shop"`
	Items        []OrderItem    `json:"items"`
	Subtotal     int64          `json:"subtotal"`
	ShippingFee  int64          `json:"shipping_fee"`
	Total        int64          `json:"total"`
	CancelReason *string        `json:"cancel_reason"`
	History      []HistoryEntry `json:"history"`
}
```

`e2e/client/endpoints.go`:

```go
package client

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) Register(ctx context.Context, email, password, fullName string) (User, error) {
	var u User
	err := c.Do(ctx, http.MethodPost, "/api/auth/register",
		map[string]string{"email": email, "password": password, "full_name": fullName}, &u, nil)
	return u, err
}

// Login stores the access token; the refresh cookie lands in the jar.
func (c *Client) Login(ctx context.Context, email, password string) (TokenResponse, error) {
	var tr TokenResponse
	err := c.Do(ctx, http.MethodPost, "/api/auth/login", map[string]string{"email": email, "password": password}, &tr, nil)
	if err == nil {
		c.token = tr.AccessToken
	}
	return tr, err
}

// Refresh rotates the refresh cookie and stores the new access token.
func (c *Client) Refresh(ctx context.Context) (TokenResponse, error) {
	var tr TokenResponse
	err := c.Do(ctx, http.MethodPost, "/api/auth/refresh", nil, &tr, nil)
	if err == nil {
		c.token = tr.AccessToken
	}
	return tr, err
}

func (c *Client) Logout(ctx context.Context) error {
	err := c.Do(ctx, http.MethodPost, "/api/auth/logout", nil, nil, nil)
	c.token = ""
	return err
}

func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	return u, c.Do(ctx, http.MethodGet, "/api/users/me", nil, &u, nil)
}

func (c *Client) Categories(ctx context.Context) ([]Category, error) {
	var cs []Category
	return cs, c.Do(ctx, http.MethodGet, "/api/categories", nil, &cs, nil)
}

func (c *Client) SearchProducts(ctx context.Context, q url.Values) (Page[ProductSummary], error) {
	var p Page[ProductSummary]
	return p, c.Do(ctx, http.MethodGet, "/api/products?"+q.Encode(), nil, &p, nil)
}

func (c *Client) Product(ctx context.Context, id string) (Product, error) {
	var p Product
	return p, c.Do(ctx, http.MethodGet, "/api/products/"+url.PathEscape(id), nil, &p, nil)
}

func (c *Client) OpenShop(ctx context.Context, name, slug, description string) (Shop, error) {
	var s Shop
	err := c.Do(ctx, http.MethodPost, "/api/shops",
		map[string]string{"name": name, "slug": slug, "description": description}, &s, nil)
	return s, err
}

func (c *Client) MyShop(ctx context.Context) (Shop, error) {
	var s Shop
	return s, c.Do(ctx, http.MethodGet, "/api/shops/me", nil, &s, nil)
}

func (c *Client) SellerProducts(ctx context.Context, cursor string) (Page[Product], error) {
	q := url.Values{"limit": {"100"}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var p Page[Product]
	return p, c.Do(ctx, http.MethodGet, "/api/seller/products?"+q.Encode(), nil, &p, nil)
}

// AllSellerProducts follows next_cursor until the last page.
func (c *Client) AllSellerProducts(ctx context.Context) ([]Product, error) {
	var all []Product
	cursor := ""
	for {
		p, err := c.SellerProducts(ctx, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, p.Items...)
		if p.NextCursor == nil {
			return all, nil
		}
		cursor = *p.NextCursor
	}
}

func (c *Client) CreateProduct(ctx context.Context, in ProductInput) (Product, error) {
	var p Product
	return p, c.Do(ctx, http.MethodPost, "/api/seller/products", in, &p, nil)
}

func (c *Client) RequestUpload(ctx context.Context, contentType string) (Upload, error) {
	var u Upload
	return u, c.Do(ctx, http.MethodPost, "/api/seller/uploads", map[string]string{"content_type": contentType}, &u, nil)
}

func (c *Client) Cart(ctx context.Context) (Cart, error) {
	var cart Cart
	return cart, c.Do(ctx, http.MethodGet, "/api/cart", nil, &cart, nil)
}

func (c *Client) SetCartItem(ctx context.Context, skuID string, quantity int) (Cart, error) {
	var cart Cart
	return cart, c.Do(ctx, http.MethodPut, "/api/cart/items/"+url.PathEscape(skuID), map[string]int{"quantity": quantity}, &cart, nil)
}

func (c *Client) RemoveCartItem(ctx context.Context, skuID string) (Cart, error) {
	var cart Cart
	return cart, c.Do(ctx, http.MethodDelete, "/api/cart/items/"+url.PathEscape(skuID), nil, &cart, nil)
}

// ClearCart removes every line, so a test or seed step starts from an empty cart.
func (c *Client) ClearCart(ctx context.Context) error {
	cart, err := c.Cart(ctx)
	if err != nil {
		return err
	}
	for _, s := range cart.Shops {
		for _, it := range s.Items {
			if _, err := c.RemoveCartItem(ctx, it.SkuID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Checkout places the order with the given Idempotency-Key; hdr adds extra
// headers (e.g. traceparent).
func (c *Client) Checkout(ctx context.Context, key string, req CheckoutRequest, hdr http.Header) (CheckoutResponse, error) {
	h := http.Header{"Idempotency-Key": {key}}
	for k, vs := range hdr {
		h[k] = vs
	}
	var res CheckoutResponse
	return res, c.Do(ctx, http.MethodPost, "/api/checkout", req, &res, h)
}

func (c *Client) Orders(ctx context.Context, status string) (Page[Order], error) {
	q := url.Values{"limit": {"100"}}
	if status != "" {
		q.Set("status", status)
	}
	var p Page[Order]
	return p, c.Do(ctx, http.MethodGet, "/api/orders?"+q.Encode(), nil, &p, nil)
}

func (c *Client) Order(ctx context.Context, id string) (Order, error) {
	var o Order
	return o, c.Do(ctx, http.MethodGet, "/api/orders/"+url.PathEscape(id), nil, &o, nil)
}

func (c *Client) CancelOrder(ctx context.Context, id, reason string) (Order, error) {
	var o Order
	return o, c.Do(ctx, http.MethodPost, "/api/orders/"+url.PathEscape(id)+"/cancel", map[string]string{"reason": reason}, &o, nil)
}

func (c *Client) CompleteOrder(ctx context.Context, id string) (Order, error) {
	var o Order
	return o, c.Do(ctx, http.MethodPost, "/api/orders/"+url.PathEscape(id)+"/complete", nil, &o, nil)
}

func (c *Client) SellerOrders(ctx context.Context, status string) (Page[Order], error) {
	q := url.Values{"limit": {"100"}}
	if status != "" {
		q.Set("status", status)
	}
	var p Page[Order]
	return p, c.Do(ctx, http.MethodGet, "/api/seller/orders?"+q.Encode(), nil, &p, nil)
}

// SellerOrderAction runs confirm, ship, deliver or cancel. reason is
// required for cancel and optional otherwise.
func (c *Client) SellerOrderAction(ctx context.Context, id, action, reason string) (Order, error) {
	body := map[string]string{}
	if reason != "" {
		body["reason"] = reason
	}
	var o Order
	return o, c.Do(ctx, http.MethodPost, "/api/seller/orders/"+url.PathEscape(id)+"/"+action, body, &o, nil)
}
```

Before Step 5, check `api/openapi/order.yaml` for the request bodies of `/complete`, `/refresh` and seller `confirm|ship|deliver`. If a request body is `required: true` where the code above sends none (or the other way round), adjust the call. Task 3's contract transport will reject any mismatch.

`e2e/client/upload.go`:

```go
package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
)

// UploadFile POSTs the file at path to a presigned POST target. Every policy
// field goes first and the file part last, as S3/MinIO POST policies require.
func UploadFile(ctx context.Context, hc *http.Client, up Upload, path, contentType string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	keys := make([]string, 0, len(up.Fields))
	for k := range up.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := w.WriteField(k, up.Fields[k]); err != nil {
			return err
		}
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filepath.Base(path)))
	h.Set("Content-Type", contentType)
	part, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, up.UploadURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("upload %s: %w", filepath.Base(path), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload %s: %d %s", filepath.Base(path), resp.StatusCode, body)
	}
	return nil
}
```

- [x] **Step 5: Tidy and run the tests to verify they pass**

Run: `cd e2e && GOWORK=off go get github.com/stretchr/testify@$(cd ../pkg && go list -m -f '{{.Version}}' github.com/stretchr/testify) && GOWORK=off go mod tidy && GOWORK=off go test -race ./client/...`
Expected: `ok  github.com/ngoxuanchien/e-commerce/e2e/client`

- [x] **Step 6: Write the image generator and generate the fixtures**

`e2e/cmd/genimages/main.go`:

```go
// Command genimages writes the deterministic product images in
// e2e/fixtures/images (run from e2e/: go run ./cmd/genimages).
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
)

type family struct {
	name  string
	count int
	base  color.RGBA
}

var families = []family{
	{"fashion", 7, color.RGBA{R: 214, G: 69, B: 65, A: 255}},
	{"electronics", 8, color.RGBA{R: 38, G: 99, B: 191, A: 255}},
	{"home", 7, color.RGBA{R: 46, G: 139, B: 87, A: 255}},
}

func main() {
	dir := filepath.Join("fixtures", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, f := range families {
		for i := 1; i <= f.count; i++ {
			path := filepath.Join(dir, fmt.Sprintf("%s-%02d.jpg", f.name, i))
			if err := write(path, f.base, i); err != nil {
				log.Fatal(err)
			}
		}
	}
}

// write draws a diagonal gradient with a disc whose position depends on i,
// so every image in a family is visibly different.
func write(path string, base color.RGBA, i int) error {
	const size = 480
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy, r := 120+(i*37)%240, 140+(i*53)%200, 70+(i*11)%40
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			t := float64(x+y) / (2 * size)
			c := color.RGBA{
				R: uint8(float64(base.R)*(1-t) + 250*t),
				G: uint8(float64(base.G)*(1-t) + 245*t),
				B: uint8(float64(base.B)*(1-t) + 235*t),
				A: 255,
			}
			if dx, dy := x-cx, y-cy; dx*dx+dy*dy < r*r {
				c = color.RGBA{R: 255, G: 255, B: 255, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, &jpeg.Options{Quality: 80})
}
```

Run: `cd e2e && GOWORK=off go run ./cmd/genimages && ls fixtures/images | wc -l && du -sh fixtures/images`
Expected: `22`, a total under 1 MB.

`e2e/fixtures/images/README.md`: one paragraph saying the images are synthetic, generated by `go run ./cmd/genimages` from `e2e/`, and used by `make seed` and the Playwright happy path.

- [x] **Step 7: Lint and commit**

Run: `cd e2e && ../bin/golangci-lint run ./...` (run `make tools` at the repo root first if `bin/` is missing)
Expected: `0 issues.`

```bash
git add e2e/go.mod e2e/go.sum e2e/client e2e/cmd/genimages e2e/fixtures
git commit -m "feat(e2e): add gateway client with 429 retry and fixture images"
```

---

### Task 2: Seed program (`make seed`)

**Files:**
- Create: `e2e/cmd/seed/{main.go,data.go,seed.go,data_test.go}`
- Modify: `Makefile` (the `seed` target only)

**Interfaces:**
- Consumes: everything from `e2e/client` (Task 1).
- Produces: demo accounts `seller1..3@example.test` and `buyer1..2@example.test` with password `matkhau123`; shops `thoi-trang-pho-co`, `dien-may-sai-gon` and `nha-xinh-da-lat`. README (Task 9) documents them.

- [x] **Step 1: Write the failing dataset test**

`e2e/cmd/seed/data_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dataset must satisfy WS6 §2 and the catalog validation rules (WS2 §3),
// or seeding fails halfway against the real stack.
func TestDatasetFollowsSpecAndCatalogRules(t *testing.T) {
	require.Len(t, sellers, 3)
	require.Len(t, buyers, 2)
	slugs := map[string]bool{}
	for _, s := range sellers {
		assert.Regexp(t, `^[a-z0-9-]{3,50}$`, s.Shop.Slug)
		assert.False(t, slugs[s.Shop.Slug], "duplicate slug %s", s.Shop.Slug)
		slugs[s.Shop.Slug] = true
		assert.GreaterOrEqual(t, len(s.Products), 6, s.Shop.Slug)
		assert.LessOrEqual(t, len(s.Products), 8, s.Shop.Slug)
		names := map[string]bool{}
		for _, p := range s.Products {
			assert.False(t, names[p.Name], "duplicate product %s", p.Name)
			names[p.Name] = true
			n := utf8.RuneCountInString(p.Name)
			assert.True(t, n >= 5 && n <= 200, "name length %q", p.Name)
			assert.NotEmpty(t, p.Category, p.Name)
			assert.True(t, len(p.Skus) >= 1 && len(p.Skus) <= 4, "sku count %q", p.Name)
			for _, sku := range p.Skus {
				assert.True(t, sku.Price >= 1_000 && sku.Price <= 1_000_000_000, "price %q", p.Name)
				assert.True(t, sku.Stock >= 0 && sku.Stock <= 100_000, "stock %q", p.Name)
			}
			_, err := os.Stat(filepath.Join("..", "..", "fixtures", "images", p.Image))
			assert.NoError(t, err, "image for %q", p.Name)
		}
	}
	statuses := map[string]bool{}
	for _, o := range orderPlans {
		require.Less(t, o.Buyer, len(buyers))
		require.Less(t, o.Shop, len(sellers))
		require.Less(t, o.Product, len(sellers[o.Shop].Products))
		statuses[o.Want] = true
	}
	assert.Equal(t, map[string]bool{"PENDING_CONFIRMATION": true, "CONFIRMED": true, "SHIPPING": true, "COMPLETED": true, "CANCELLED": true}, statuses)
}
```

- [x] **Step 2: Run it to verify it fails**

Run: `cd e2e && GOWORK=off go test ./cmd/seed/...`
Expected: FAIL to compile (`undefined: sellers`).

- [x] **Step 3: Write the dataset**

`e2e/cmd/seed/data.go`:

```go
package main

import "github.com/ngoxuanchien/e-commerce/e2e/client"

// password is shared by every synthetic demo account.
const password = "matkhau123"

type account struct {
	Email    string
	FullName string
}

type shopData struct {
	Name, Slug, Description string
}

type productData struct {
	Name        string
	Description string
	Category    string // child category slug seeded by catalog migration 000003
	Image       string // file in e2e/fixtures/images
	Skus        []client.SkuInput
}

type sellerData struct {
	Account  account
	Shop     shopData
	Products []productData
}

// orderPlan places one single-line order and walks it to Want. Steps are
// seller actions (confirm, ship, deliver) or buyer actions (complete, cancel).
type orderPlan struct {
	Buyer, Shop, Product int
	Steps                []string
	Want                 string
}

func sku(name string, price int64, stock int) client.SkuInput {
	return client.SkuInput{Name: name, Price: price, Stock: stock, Active: true}
}

var buyers = []account{
	{Email: "buyer1@example.test", FullName: "An Nguyễn"},
	{Email: "buyer2@example.test", FullName: "Bình Hoàng"},
}

var sellers = []sellerData{
	{
		Account: account{Email: "seller1@example.test", FullName: "Lan Trần"},
		Shop:    shopData{Name: "Thời Trang Phố Cổ", Slug: "thoi-trang-pho-co", Description: "Quần áo cotton may tại Hà Nội, giao toàn quốc."},
		Products: []productData{
			{"Áo thun cotton trơn cổ tròn", "Cotton 100%, thấm hút mồ hôi, form regular.", "ao-thun-nam", "fashion-01.jpg",
				[]client.SkuInput{sku("S", 149_000, 50), sku("M", 149_000, 50), sku("L", 149_000, 40), sku("XL", 159_000, 30)}},
			{"Áo sơ mi linen tay dài", "Vải linen thoáng mát, phù hợp đi làm.", "ao-so-mi-nam", "fashion-02.jpg",
				[]client.SkuInput{sku("M", 329_000, 30), sku("L", 329_000, 25)}},
			{"Quần jeans nam ống đứng", "Denim co giãn nhẹ, wash xanh đậm.", "quan-jeans-nam", "fashion-03.jpg",
				[]client.SkuInput{sku("29", 459_000, 20), sku("30", 459_000, 20), sku("31", 459_000, 15), sku("32", 459_000, 10)}},
			{"Váy hoa nhí dáng xòe", "Voan hai lớp, dây kéo sau lưng.", "vay-dam", "fashion-04.jpg",
				[]client.SkuInput{sku("S", 389_000, 25), sku("M", 389_000, 25)}},
			{"Áo len nữ cổ lọ", "Len dệt kim mềm, giữ ấm mùa đông.", "ao-nu", "fashion-05.jpg",
				[]client.SkuInput{sku("Kem", 279_000, 15), sku("Xám", 279_000, 15)}},
			{"Quần culottes nữ lưng cao", "Ống rộng, vải tuyết mưa không nhăn.", "quan-nu", "fashion-06.jpg",
				[]client.SkuInput{sku("S", 259_000, 40), sku("M", 259_000, 40), sku("L", 259_000, 30)}},
			{"Áo polo nam phối viền", "Cá sấu cotton, cổ bẻ phối viền.", "ao-thun-nam", "fashion-07.jpg",
				[]client.SkuInput{sku("M", 219_000, 35), sku("L", 219_000, 35)}},
		},
	},
	{
		Account: account{Email: "seller2@example.test", FullName: "Hùng Lê"},
		Shop:    shopData{Name: "Điện Máy Sài Gòn", Slug: "dien-may-sai-gon", Description: "Phụ kiện và thiết bị điện tử chính hãng, bảo hành 12 tháng."},
		Products: []productData{
			{"Tai nghe bluetooth chống ồn", "Chống ồn chủ động, pin 30 giờ.", "tai-nghe", "electronics-01.jpg",
				[]client.SkuInput{sku("Đen", 1_290_000, 30), sku("Trắng", 1_290_000, 20)}},
			{"Ốp lưng silicon trong suốt", "Chống ố vàng, viền bảo vệ camera.", "op-lung", "electronics-02.jpg",
				[]client.SkuInput{sku("iPhone 16", 89_000, 100), sku("Galaxy S26", 89_000, 80)}},
			{"Củ sạc nhanh GaN 65W", "Hai cổng USB-C, một cổng USB-A.", "sac-cap", "electronics-03.jpg",
				[]client.SkuInput{sku("Tiêu chuẩn", 590_000, 60)}},
			{"Chuột không dây công thái học", "Kết nối 2.4 GHz và bluetooth.", "chuot-ban-phim", "electronics-04.jpg",
				[]client.SkuInput{sku("Tiêu chuẩn", 450_000, 35)}},
			{"Bàn phím cơ 87 phím", "Hot-swap, đèn nền RGB.", "chuot-ban-phim", "electronics-05.jpg",
				[]client.SkuInput{sku("Switch đỏ", 1_150_000, 20), sku("Switch nâu", 1_150_000, 20)}},
			{"Loa bluetooth mini chống nước", "Chuẩn IPX7, pin 12 giờ.", "loa", "electronics-06.jpg",
				[]client.SkuInput{sku("Xanh", 690_000, 25), sku("Đỏ", 690_000, 25), sku("Đen", 690_000, 25)}},
			{"Đồng hồ thông minh theo dõi sức khỏe", "Đo nhịp tim, SpO2, GPS tích hợp.", "dong-ho-thong-minh", "electronics-07.jpg",
				[]client.SkuInput{sku("41mm", 2_490_000, 10), sku("45mm", 2_690_000, 10)}},
			{"Màn hình 24 inch IPS 75Hz", "Tấm nền IPS, viền mỏng ba cạnh.", "man-hinh", "electronics-08.jpg",
				[]client.SkuInput{sku("Tiêu chuẩn", 2_890_000, 8)}},
		},
	},
	{
		Account: account{Email: "seller3@example.test", FullName: "Mai Phạm"},
		Shop:    shopData{Name: "Nhà Xinh Đà Lạt", Slug: "nha-xinh-da-lat", Description: "Đồ gia dụng và trang trí thủ công từ Đà Lạt."},
		Products: []productData{
			{"Bát ăn cơm men lam Bát Tràng", "Gốm Bát Tràng vẽ tay men lam.", "do-dung-nha-bep", "home-01.jpg",
				[]client.SkuInput{sku("Bộ 6 chiếc", 180_000, 40), sku("Bộ 10 chiếc", 280_000, 20)}},
			{"Chảo chống dính đáy từ", "Dùng được bếp từ, phủ ceramic.", "do-dung-nha-bep", "home-02.jpg",
				[]client.SkuInput{sku("26cm", 350_000, 30), sku("28cm", 390_000, 20)}},
			{"Bình hoa gốm men rạn cổ", "Men rạn thủ công, cao 30 cm.", "trang-tri-nha-cua", "home-03.jpg",
				[]client.SkuInput{sku("Tiêu chuẩn", 420_000, 12)}},
			{"Nến thơm tinh dầu sả chanh", "Sáp đậu nành, tinh dầu thiên nhiên.", "trang-tri-nha-cua", "home-04.jpg",
				[]client.SkuInput{sku("120g", 95_000, 50), sku("200g", 145_000, 40)}},
			{"Bộ chăn ga cotton lụa", "Cotton lụa mát, bốn món.", "chan-ga-goi", "home-05.jpg",
				[]client.SkuInput{sku("1m6", 890_000, 15), sku("1m8", 990_000, 15)}},
			{"Gối tựa lưng sofa vải bố", "Vỏ tháo rời, ruột bông gòn.", "trang-tri-nha-cua", "home-06.jpg",
				[]client.SkuInput{sku("Be", 159_000, 30), sku("Xanh rêu", 159_000, 30)}},
			{"Thớt gỗ nghiến tự nhiên", "Gỗ nghiến nguyên khối, không nứt.", "do-dung-nha-bep", "home-07.jpg",
				[]client.SkuInput{sku("Nhỏ", 120_000, 25), sku("Vừa", 180_000, 25), sku("Lớn", 250_000, 15)}},
		},
	},
}

var orderPlans = []orderPlan{
	{Buyer: 0, Shop: 0, Product: 0, Want: "PENDING_CONFIRMATION"},
	{Buyer: 0, Shop: 1, Product: 0, Steps: []string{"confirm"}, Want: "CONFIRMED"},
	{Buyer: 1, Shop: 2, Product: 0, Steps: []string{"confirm", "ship"}, Want: "SHIPPING"},
	{Buyer: 1, Shop: 0, Product: 1, Steps: []string{"confirm", "ship", "deliver", "complete"}, Want: "COMPLETED"},
	{Buyer: 0, Shop: 2, Product: 1, Steps: []string{"cancel"}, Want: "CANCELLED"},
}
```

(Positional `productData` literals are fine here: the struct is local and the test pins the fields. If `govet`'s `composites` check flags them, switch to keyed fields.)

- [x] **Step 4: Write the seeding logic and entry point**

`e2e/cmd/seed/seed.go`:

```go
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

type seeder struct {
	gateway  string
	fixtures string
	rt       http.RoundTripper
	upload   *http.Client
	out      io.Writer

	categories map[string]string // slug -> id
}

type sellerState struct {
	c        *client.Client
	shop     client.Shop
	products map[string]client.Product // by name
}

func (s *seeder) run(ctx context.Context) error {
	if err := waitReady(ctx, s.gateway+"/readyz", 90*time.Second); err != nil {
		return err
	}
	if err := s.loadCategories(ctx); err != nil {
		return err
	}
	shops := make([]sellerState, len(sellers))
	for i, sd := range sellers {
		st, err := s.seller(ctx, sd)
		if err != nil {
			return fmt.Errorf("seller %s: %w", sd.Account.Email, err)
		}
		shops[i] = st
	}
	bs := make([]*client.Client, len(buyers))
	for i, a := range buyers {
		c, err := s.account(ctx, a)
		if err != nil {
			return err
		}
		bs[i] = c
	}
	if err := s.orders(ctx, shops, bs); err != nil {
		return err
	}
	s.summary()
	return nil
}

func (s *seeder) loadCategories(ctx context.Context) error {
	cats, err := client.New(s.gateway, s.rt).Categories(ctx)
	if err != nil {
		return fmt.Errorf("categories: %w", err)
	}
	s.categories = map[string]string{}
	for _, top := range cats {
		s.categories[top.Slug] = top.ID
		for _, ch := range top.Children {
			s.categories[ch.Slug] = ch.ID
		}
	}
	return nil
}

// account registers a (registering an existing email is a 409 we accept)
// and logs in.
func (s *seeder) account(ctx context.Context, a account) (*client.Client, error) {
	c := client.New(s.gateway, s.rt)
	if _, err := c.Register(ctx, a.Email, password, a.FullName); err != nil && !client.IsCode(err, "CONFLICT") {
		return nil, fmt.Errorf("register %s: %w", a.Email, err)
	}
	if _, err := c.Login(ctx, a.Email, password); err != nil {
		return nil, fmt.Errorf("login %s: %w", a.Email, err)
	}
	return c, nil
}

// seller ensures the account, its shop and every product exist. It resumes a
// partial earlier run: an existing shop is kept and only missing products
// (matched by name) are created.
func (s *seeder) seller(ctx context.Context, sd sellerData) (sellerState, error) {
	c, err := s.account(ctx, sd.Account)
	if err != nil {
		return sellerState{}, err
	}
	shop, err := c.MyShop(ctx)
	switch st := client.StatusOf(err); {
	case err == nil:
		fmt.Fprintf(s.out, "shop %s: exists\n", shop.Slug)
	case st == http.StatusForbidden || st == http.StatusNotFound: // no seller role yet / no shop
		if shop, err = c.OpenShop(ctx, sd.Shop.Name, sd.Shop.Slug, sd.Shop.Description); err != nil {
			return sellerState{}, fmt.Errorf("open shop: %w", err)
		}
		if _, err := c.Refresh(ctx); err != nil { // the new token carries the seller role
			return sellerState{}, fmt.Errorf("refresh after opening shop: %w", err)
		}
		fmt.Fprintf(s.out, "shop %s: created\n", shop.Slug)
	default:
		return sellerState{}, fmt.Errorf("my shop: %w", err)
	}

	existing, err := c.AllSellerProducts(ctx)
	if err != nil {
		return sellerState{}, fmt.Errorf("list products: %w", err)
	}
	st := sellerState{c: c, shop: shop, products: map[string]client.Product{}}
	for _, p := range existing {
		st.products[p.Name] = p
	}
	created := 0
	for _, pd := range sd.Products {
		if _, ok := st.products[pd.Name]; ok {
			continue
		}
		p, err := s.createProduct(ctx, c, pd)
		if err != nil {
			return sellerState{}, fmt.Errorf("product %q: %w", pd.Name, err)
		}
		st.products[p.Name] = p
		created++
	}
	fmt.Fprintf(s.out, "shop %s: %d products created, %d already present\n", shop.Slug, created, len(sd.Products)-created)
	return st, nil
}

func (s *seeder) createProduct(ctx context.Context, c *client.Client, pd productData) (client.Product, error) {
	catID, ok := s.categories[pd.Category]
	if !ok {
		return client.Product{}, fmt.Errorf("unknown category %q", pd.Category)
	}
	up, err := c.RequestUpload(ctx, "image/jpeg")
	if err != nil {
		return client.Product{}, fmt.Errorf("request upload: %w", err)
	}
	if err := client.UploadFile(ctx, s.upload, up, filepath.Join(s.fixtures, pd.Image), "image/jpeg"); err != nil {
		return client.Product{}, err
	}
	return c.CreateProduct(ctx, client.ProductInput{
		CategoryID: catID, Name: pd.Name, Description: pd.Description, Status: "active",
		ImageKeys: []string{up.ObjectKey}, Skus: pd.Skus,
	})
}

// orders creates the demo orders once: if buyer1 already has any order, the
// whole phase is skipped so re-runs never pile up duplicates.
func (s *seeder) orders(ctx context.Context, shops []sellerState, bs []*client.Client) error {
	page, err := bs[0].Orders(ctx, "")
	if err != nil {
		return fmt.Errorf("list orders: %w", err)
	}
	if len(page.Items) > 0 {
		fmt.Fprintln(s.out, "orders: exist, skipped")
		return nil
	}
	for _, op := range orderPlans {
		b, seller := bs[op.Buyer], shops[op.Shop]
		p := seller.products[sellers[op.Shop].Products[op.Product].Name]
		skuID := p.Skus[0].ID
		if err := b.ClearCart(ctx); err != nil {
			return err
		}
		if _, err := b.SetCartItem(ctx, skuID, 1); err != nil {
			return fmt.Errorf("cart %q: %w", p.Name, err)
		}
		res, err := b.Checkout(ctx, newKey(), client.CheckoutRequest{
			SkuIDs: []string{skuID}, ShippingAddress: client.DemoAddress(buyers[op.Buyer].FullName), PaymentMethod: "COD",
		}, nil)
		if err != nil {
			return fmt.Errorf("checkout %q: %w", p.Name, err)
		}
		o := res.Orders[0]
		for _, step := range op.Steps {
			switch step {
			case "complete":
				o, err = b.CompleteOrder(ctx, o.ID)
			case "cancel":
				o, err = b.CancelOrder(ctx, o.ID, "Đặt nhầm phân loại")
			default:
				o, err = seller.c.SellerOrderAction(ctx, o.ID, step, "")
			}
			if err != nil {
				return fmt.Errorf("order %s %s: %w", o.ID, step, err)
			}
		}
		if o.Status != op.Want {
			return fmt.Errorf("order %s: status %s, want %s", o.ID, o.Status, op.Want)
		}
		fmt.Fprintf(s.out, "order %s (%s): %s\n", o.ID, p.Name, o.Status)
	}
	return nil
}

func (s *seeder) summary() {
	fmt.Fprintln(s.out, "\nDemo accounts (password: "+password+")")
	w := tabwriter.NewWriter(s.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tEMAIL\tNAME\tSHOP")
	for _, sd := range sellers {
		fmt.Fprintf(w, "seller\t%s\t%s\t/shops/%s\n", sd.Account.Email, sd.Account.FullName, sd.Shop.Slug)
	}
	for _, a := range buyers {
		fmt.Fprintf(w, "buyer\t%s\t%s\t-\n", a.Email, a.FullName)
	}
	_ = w.Flush()
	fmt.Fprintln(s.out, "\nWeb: http://localhost:3000  Gateway: http://localhost:8080  Jaeger: http://localhost:16686  MinIO console: http://localhost:9001")
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on supported platforms
	return "seed-" + hex.EncodeToString(b)
}

func waitReady(ctx context.Context, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gateway not ready at %s (run `make up-app` first)", url)
		case <-time.After(time.Second):
		}
	}
}
```

`e2e/cmd/seed/main.go`:

```go
// Command seed fills a running stack with demo data through the public
// gateway API (WS6 §2). It is idempotent: re-running only creates what is
// missing. Run from e2e/: go run ./cmd/seed.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

func main() {
	gateway := flag.String("gateway", envOr("GATEWAY_URL", "http://localhost:8080"), "gateway base URL")
	fixtures := flag.String("fixtures", "fixtures/images", "directory with product images")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	s := &seeder{
		gateway:  *gateway,
		fixtures: *fixtures,
		rt:       client.RetryRateLimited{MaxWait: 3 * time.Minute},
		upload:   &http.Client{Timeout: 30 * time.Second},
		out:      os.Stdout,
	}
	if err := s.run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [x] **Step 5: Run the dataset test to verify it passes**

Run: `cd e2e && GOWORK=off go vet ./... && GOWORK=off go test ./cmd/seed/...`
Expected: `ok  github.com/ngoxuanchien/e-commerce/e2e/cmd/seed`

- [x] **Step 6: Replace the Makefile `seed` target and run it twice against the real stack**

In `Makefile`, replace only the `seed` target:

```make
seed: ## Seed demo data through the gateway (run after make up-app; idempotent)
	cd e2e && GOWORK=off go run ./cmd/seed
```

Check whether the stack is up: `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.web.yml --profile app ps`. If it isn't, run `make up-app` from this worktree. The worktree has every service merged, so its migrations match the shared volume. If another worktree's stack is already running, ask the user before recreating it.

Run: `make seed`
Expected: 3 × `shop …: created`, 3 × `N products created`, 5 × `order … : <STATUS>` covering the five statuses, then the accounts table. Exit 0.

Run: `make seed` again. Then check the counts:

```bash
curl -s 'localhost:8080/api/products?shop=dien-may-sai-gon&limit=100' | jq '.items | length'   # 8
curl -s 'localhost:8080/api/products?q=ao%20thun&limit=100' | jq '[.items[].name]'           # contains "Áo thun cotton trơn cổ tròn"
curl -sI "$(curl -s 'localhost:8080/api/products?shop=nha-xinh-da-lat' | jq -r '.items[0].image_url')" | head -1  # HTTP/1.1 200 OK
```

Expected for the second run: 3 × `shop …: exists`, `0 products created, N already present`, `orders: exist, skipped`, exit 0. The counts are unchanged.

If seeding fails against the real stack for a reason inside a service, follow the Findings protocol in Global Constraints. Do not patch the service.

- [x] **Step 7: Commit**

```bash
git add e2e/cmd/seed Makefile
git commit -m "feat(e2e): add idempotent seed program behind make seed"
```

---

### Task 3: API smoke test harness, happy-path flow and auth checks

**Files:**
- Create: `e2e/api/{main_test.go,harness_test.go,flow_test.go,auth_test.go,findings_test.go}`

**Interfaces:**
- Consumes: `e2e/client`; `pkg/testutil.ContractTransport(t testing.TB, specPath string, base http.RoundTripper) http.RoundTripper`.
- Produces (for Tasks 4–5, same package `api_test`):
  - `getWorld(t) *world` with the fields `run string`, `sellerA, sellerB actor`, `shopA, shopB client.Shop`, `buyers []actor` (5), `categoryID string`.
  - `actor{*client.Client; User client.User; Email string}`.
  - `newActor(t, email, fullName) actor`, `newSeller(t, run, tag) (actor, client.Shop)`.
  - `newProduct(t, seller actor, categoryID, name string, stocks ...int) client.Product`. It makes one SKU per stock value, named `Loại 1..n`, price 100,000.
  - `stockOf(t, productID, skuID) int`, `newKey() string`, `runID() string`, `ctx(t) context.Context`, `envOr(key, def) string`.
  - Constants and vars `password`, `shippingFee = 30_000`, `gatewayURL`, `jaegerURL`.

- [x] **Step 1: Write the harness**

`e2e/api/main_test.go`:

```go
// Package api_test holds the API smoke tests: the P1 flows run against the
// real stack through the gateway, and every call is checked against the
// OpenAPI contract.
package api_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

var (
	gatewayURL = envOr("GATEWAY_URL", "http://localhost:8080")
	jaegerURL  = envOr("JAEGER_URL", "http://localhost:16686")
)

func TestMain(m *testing.M) {
	if err := waitReady(gatewayURL+"/readyz", 90*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func waitReady(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gateway not ready at %s — start the stack with `make up-app`", url)
		case <-time.After(time.Second):
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

`e2e/api/harness_test.go`:

```go
package api_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

const (
	password    = "matkhau123"
	shippingFee = int64(30_000)
)

// contractRouter validates each call against the OpenAPI document of the
// service that owns its path (WS4 §2 routing table).
type contractRouter struct{ identity, catalog, order http.RoundTripper }

func (c contractRouter) RoundTrip(r *http.Request) (*http.Response, error) {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/auth/"), strings.HasPrefix(p, "/api/users/"):
		return c.identity.RoundTrip(r)
	case strings.HasPrefix(p, "/api/cart"), p == "/api/checkout",
		strings.HasPrefix(p, "/api/orders"), strings.HasPrefix(p, "/api/seller/orders"):
		return c.order.RoundTrip(r)
	default:
		return c.catalog.RoundTrip(r)
	}
}

// newTransport validates the contract above a 429-retrying transport, so the
// contract check only sees final responses.
func newTransport(t *testing.T) http.RoundTripper {
	t.Helper()
	base := client.RetryRateLimited{MaxWait: 3 * time.Minute}
	spec := func(name string) http.RoundTripper {
		return testutil.ContractTransport(t, filepath.Join("..", "..", "api", "openapi", name), base)
	}
	return contractRouter{identity: spec("identity.yaml"), catalog: spec("catalog.yaml"), order: spec("order.yaml")}
}

type actor struct {
	*client.Client
	User  client.User
	Email string
}

func ctx(t *testing.T) context.Context { return t.Context() }

func newActor(t *testing.T, email, fullName string) actor {
	t.Helper()
	c := client.New(gatewayURL, newTransport(t))
	_, err := c.Register(ctx(t), email, password, fullName)
	require.NoError(t, err, "register %s", email)
	tr, err := c.Login(ctx(t), email, password)
	require.NoError(t, err, "login %s", email)
	return actor{Client: c, User: tr.User, Email: email}
}

func newSeller(t *testing.T, run, tag string) (actor, client.Shop) {
	t.Helper()
	a := newActor(t, fmt.Sprintf("e2e-%s-seller-%s@example.test", run, tag), "Người bán "+strings.ToUpper(tag))
	shop, err := a.OpenShop(ctx(t), "Shop E2E "+run+" "+tag, "e2e-"+run+"-"+tag, "Shop tạo bởi e2e.")
	require.NoError(t, err)
	tr, err := a.Refresh(ctx(t))
	require.NoError(t, err)
	require.Contains(t, tr.User.Roles, "seller")
	a.User = tr.User
	return a, shop
}

func newProduct(t *testing.T, seller actor, categoryID, name string, stocks ...int) client.Product {
	t.Helper()
	skus := make([]client.SkuInput, len(stocks))
	for i, s := range stocks {
		skus[i] = client.SkuInput{Name: fmt.Sprintf("Loại %d", i+1), Price: 100_000, Stock: s, Active: true}
	}
	p, err := seller.CreateProduct(ctx(t), client.ProductInput{
		CategoryID: categoryID, Name: name, Description: name + " — sản phẩm kiểm thử.",
		Status: "active", ImageKeys: []string{}, Skus: skus,
	})
	require.NoError(t, err)
	return p
}

// stockOf reads on-hand stock through the public product endpoint.
func stockOf(t *testing.T, productID, skuID string) int {
	t.Helper()
	p, err := client.New(gatewayURL, newTransport(t)).Product(ctx(t), productID)
	require.NoError(t, err)
	for _, s := range p.Skus {
		if s.ID == skuID {
			return s.Stock
		}
	}
	t.Fatalf("sku %s not in product %s", skuID, productID)
	return 0
}

// runID is 8 lowercase letters: one search token, valid in slugs and emails.
func runID() string {
	b := make([]byte, 8)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(26))
		b[i] = byte('a' + n.Int64())
	}
	return string(b)
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// world is shared by every test in the package. Registering and logging in
// share a 10/min login bucket per client IP, so users are created once per
// run; each test uses its own products and clears its buyers' carts.
type world struct {
	run          string
	sellerA      actor
	sellerB      actor
	shopA, shopB client.Shop
	buyers       []actor
	categoryID   string
}

var (
	worldOnce sync.Once
	theWorld  *world
)

func getWorld(t *testing.T) *world {
	t.Helper()
	worldOnce.Do(func() { theWorld = buildWorld(t) })
	if theWorld == nil {
		t.Fatal("shared world could not be built (see the first failing test)")
	}
	return theWorld
}

func buildWorld(t *testing.T) *world {
	w := &world{run: runID()}
	w.sellerA, w.shopA = newSeller(t, w.run, "a")
	w.sellerB, w.shopB = newSeller(t, w.run, "b")
	for i := range 5 {
		w.buyers = append(w.buyers, newActor(t, fmt.Sprintf("e2e-%s-buyer%d@example.test", w.run, i), fmt.Sprintf("Người mua %d", i)))
	}
	cats, err := w.sellerA.Categories(ctx(t))
	require.NoError(t, err)
	for _, c := range cats {
		for _, ch := range c.Children {
			if ch.Slug == "do-dung-nha-bep" {
				w.categoryID = ch.ID
			}
		}
	}
	require.NotEmpty(t, w.categoryID)
	return w
}
```

Notes:
- `ContractTransport` calls `t.Fatalf` only while loading the specs. The shared world's clients stay usable after the first test ends, because the transports never touch `t` again.
- `ctx(t)` returns `t.Context()`. Shared clients created during test A take that test's context only per call, never at construction, so they're safe to use in test B.

- [x] **Step 2: Write the flow test**

`e2e/api/flow_test.go`:

```go
package api_test

import (
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// Success criteria 1–4: search (with and without diacritics), a cart from two
// shops, checkout into one COD order per shop, the full seller/buyer
// lifecycle, and a presigned image upload served back from MinIO.
func TestCheckoutAcrossTwoShopsAndLifecycle(t *testing.T) {
	w := getWorld(t)
	buyer := w.buyers[0]
	require.NoError(t, buyer.ClearCart(ctx(t)))

	// Seller A lists a product with an uploaded image and two variants.
	up, err := w.sellerA.RequestUpload(ctx(t), "image/jpeg")
	require.NoError(t, err)
	require.NoError(t, client.UploadFile(ctx(t), http.DefaultClient, up, filepath.Join("..", "fixtures", "images", "home-01.jpg"), "image/jpeg"))
	pa, err := w.sellerA.CreateProduct(ctx(t), client.ProductInput{
		CategoryID: w.categoryID, Name: "Bát gốm men lam " + w.run, Description: "Gốm vẽ tay.", Status: "active",
		ImageKeys: []string{up.ObjectKey},
		Skus: []client.SkuInput{
			{Name: "Bộ nhỏ", Price: 150_000, Stock: 10, Active: true},
			{Name: "Bộ lớn", Price: 250_000, Stock: 10, Active: true},
		},
	})
	require.NoError(t, err)
	require.Len(t, pa.Images, 1)
	img, err := http.Get(pa.Images[0].URL)
	require.NoError(t, err)
	_ = img.Body.Close()
	assert.Equal(t, http.StatusOK, img.StatusCode, "uploaded image is publicly readable")

	pb := newProduct(t, w.sellerB, w.categoryID, "Đèn ngủ gỗ thông "+w.run, 10)

	// Search without and with diacritics both find the product.
	for _, q := range []string{"bat gom men lam " + w.run, "Bát gốm " + w.run} {
		page, err := buyer.SearchProducts(ctx(t), url.Values{"q": {q}})
		require.NoError(t, err)
		require.Len(t, page.Items, 1, "query %q", q)
		assert.Equal(t, pa.ID, page.Items[0].ID)
		require.NotNil(t, page.Items[0].ImageURL)
	}
	detail, err := buyer.Product(ctx(t), pa.ID)
	require.NoError(t, err)
	assert.Len(t, detail.Skus, 2)

	// Cart with lines from two shops.
	large := pa.Skus[1]
	_, err = buyer.SetCartItem(ctx(t), large.ID, 2)
	require.NoError(t, err)
	cart, err := buyer.SetCartItem(ctx(t), pb.Skus[0].ID, 1)
	require.NoError(t, err)
	require.Len(t, cart.Shops, 2)

	// Checkout: one COD order per shop, sharing the checkout id.
	res, err := buyer.Checkout(ctx(t), newKey(), client.CheckoutRequest{ShippingAddress: client.DemoAddress("Người mua 0"), PaymentMethod: "COD"}, nil)
	require.NoError(t, err)
	require.Len(t, res.Orders, 2)
	byShop := map[string]client.Order{}
	for _, o := range res.Orders {
		assert.Equal(t, res.CheckoutID, o.CheckoutID)
		assert.Equal(t, "PENDING_CONFIRMATION", o.Status)
		assert.Equal(t, shippingFee, o.ShippingFee)
		assert.Equal(t, o.Subtotal+o.ShippingFee, o.Total)
		byShop[o.Shop.ID] = o
	}
	orderA, orderB := byShop[w.shopA.ID], byShop[w.shopB.ID]
	require.NotEmpty(t, orderA.ID)
	require.NotEmpty(t, orderB.ID)
	assert.Equal(t, 2*large.Price, orderA.Subtotal)

	cart, err = buyer.Cart(ctx(t))
	require.NoError(t, err)
	assert.Empty(t, cart.Shops, "purchased lines leave the cart")
	assert.Equal(t, 8, stockOf(t, pa.ID, large.ID), "checkout reserves stock synchronously")

	// Seller B cannot act on seller A's order; seller A walks it to DELIVERED.
	_, err = w.sellerB.SellerOrderAction(ctx(t), orderA.ID, "confirm", "")
	assert.Equal(t, http.StatusNotFound, client.StatusOf(err))
	for _, step := range []struct{ action, want string }{{"confirm", "CONFIRMED"}, {"ship", "SHIPPING"}, {"deliver", "DELIVERED"}} {
		o, err := w.sellerA.SellerOrderAction(ctx(t), orderA.ID, step.action, "")
		require.NoError(t, err, step.action)
		assert.Equal(t, step.want, o.Status)
	}
	done, err := buyer.CompleteOrder(ctx(t), orderA.ID)
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", done.Status)

	var to []string
	for _, h := range done.History {
		to = append(to, h.To)
	}
	assert.Subset(t, to, []string{"PENDING_CONFIRMATION", "CONFIRMED", "SHIPPING", "DELIVERED", "COMPLETED"})
	assert.Equal(t, "COMPLETED", to[len(to)-1])

	// Seller B sees its own order.
	sellerOrders, err := w.sellerB.SellerOrders(ctx(t), "PENDING_CONFIRMATION")
	require.NoError(t, err)
	var ids []string
	for _, o := range sellerOrders.Items {
		ids = append(ids, o.ID)
	}
	assert.Contains(t, ids, orderB.ID)
}
```

- [x] **Step 3: Write the auth test**

`e2e/api/auth_test.go`:

```go
package api_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

func TestGatewayAuthBoundaries(t *testing.T) {
	w := getWorld(t)
	anon := client.New(gatewayURL, newTransport(t))

	_, err := anon.Me(ctx(t))
	assert.True(t, client.IsCode(err, "UNAUTHENTICATED"), "anonymous /api/users/me: %v", err)

	// A spoofed identity header without a token is stripped: still 401.
	err = anon.Do(ctx(t), http.MethodGet, "/api/cart", nil, nil, http.Header{"X-User-Id": {w.buyers[0].User.ID}})
	assert.True(t, client.IsCode(err, "UNAUTHENTICATED"), "spoofed X-User-Id: %v", err)

	// An invalid token fails loudly even on a public route.
	bad := client.New(gatewayURL, newTransport(t))
	bad.SetToken("not-a-jwt")
	_, err = bad.SearchProducts(ctx(t), url.Values{})
	assert.True(t, client.IsCode(err, "UNAUTHENTICATED"), "garbage token on public route: %v", err)

	// A buyer is not a seller.
	_, err = w.buyers[0].SellerProducts(ctx(t), "")
	assert.True(t, client.IsCode(err, "FORBIDDEN"), "buyer on seller route: %v", err)

	me, err := w.buyers[0].Me(ctx(t))
	require.NoError(t, err)
	assert.Equal(t, w.buyers[0].User.ID, me.ID)
}

// Refresh rotation (WS1 §3): reusing a rotated refresh token revokes the
// whole family; logout ends the session.
func TestRefreshRotationReuseDetectionAndLogout(t *testing.T) {
	run := runID()
	a := newActor(t, "e2e-"+run+"-auth@example.test", "Người dùng Auth")
	refreshURL, _ := url.Parse(gatewayURL + "/api/auth/refresh")
	old := a.HTTP.Jar.Cookies(refreshURL)
	require.NotEmpty(t, old, "login set the refresh cookie")

	_, err := a.Refresh(ctx(t))
	require.NoError(t, err)

	thief := client.New(gatewayURL, newTransport(t))
	thief.HTTP.Jar.SetCookies(refreshURL, old)
	_, err = thief.Refresh(ctx(t))
	assert.Equal(t, http.StatusUnauthorized, client.StatusOf(err), "rotated token is rejected")

	_, err = a.Refresh(ctx(t))
	assert.Equal(t, http.StatusUnauthorized, client.StatusOf(err), "reuse revoked the whole family")

	_, err = a.Login(ctx(t), a.Email, password)
	require.NoError(t, err)
	require.NoError(t, a.Logout(ctx(t)))
	_, err = a.Refresh(ctx(t))
	assert.Equal(t, http.StatusUnauthorized, client.StatusOf(err), "logout revoked the session")
}
```

`cookiejar.Cookies` returns name/value only, and re-setting it on `refreshURL` scopes it to that path. If the identity service rejects the replayed cookie for a reason other than reuse (for example, the path doesn't match `/api/auth`), set the cookie on `gatewayURL + "/api/auth/"` instead.

- [x] **Step 4: Run the API tests against the running stack**

Run: `cd e2e && GOWORK=off go test -count=1 -v -run 'TestCheckoutAcrossTwoShops|TestGatewayAuth|TestRefreshRotation' ./api/`
Expected: PASS. The first run may pause on 429s while the world is built; that's expected (`RetryRateLimited`).

- [x] **Step 5: Triage failures with the Findings protocol**

For each failure: is it a test bug (wrong assumption about UI copy, contract, or behavior the spec leaves open), or a service/contract bug?
- **Test bug:** fix the test.
- **Contract violation** (a `response violates contract` error from `ContractTransport`) **or a behavior bug:** use superpowers:systematic-debugging. Collect the failing request/response, `docker compose … logs <svc>`, and the owning code line. Add a row to "Findings". Move the reproducing assertion into `findings_test.go`, gated by `requireFindings`, so the main test keeps covering everything else.

- [x] **Step 6: Write the findings harness and the F1 reproduction**

`e2e/api/findings_test.go`:

```go
package api_test

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Tests in this file reproduce bugs reported to their owning workstream (see
// the Findings table in docs/superpowers/plans/2026-09-28-ws6-integration.md).
// They fail until the owner fixes the bug, so they only run with E2E_FINDINGS=1.
func requireFindings(t *testing.T, id string) {
	t.Helper()
	if os.Getenv("E2E_FINDINGS") != "1" {
		t.Skipf("finding %s: set E2E_FINDINGS=1 to run the reproduction", id)
	}
}

// F1: through the web container every browser shares nginx's IP, so eleven
// logins from eleven different clients exhaust one 10/min bucket.
func TestBrowserClientsHaveSeparateLoginBuckets(t *testing.T) {
	requireFindings(t, "F1")
	webURL := envOr("WEB_URL", "http://localhost:3000")
	limited := 0
	for i := range 11 {
		req, _ := http.NewRequestWithContext(ctx(t), http.MethodPost, webURL+"/api/auth/login",
			strings.NewReader(`{"email":"nobody@example.test","password":"matkhau123"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1)) // TEST-NET-3: eleven distinct clients
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			limited++
		}
	}
	assert.Zero(t, limited, "distinct clients behind the web proxy must not share a login bucket")
}
```

This test burns the web origin's login bucket, so don't run it right before Playwright.

- [x] **Step 7: Confirm or drop F1**

Run: `cd e2e && E2E_FINDINGS=1 GOWORK=off go test -count=1 -v -run TestBrowserClientsHaveSeparateLoginBuckets ./api/`
- If it FAILS with `limited > 0`, F1 is confirmed. Add the evidence to the table: the output, the `TRUSTED_PROXIES: ""` line in `deploy/docker-compose.yml`, and `services/gateway/internal/ratelimit/clientip.go` `Resolve`. Report it to the user.
- If it PASSES, delete the test and the F1 candidate paragraph.

Then wait 60 s before running any Playwright tests.

- [x] **Step 8: Lint and commit**

Run: `cd e2e && ../bin/golangci-lint run ./... && GOWORK=off go vet ./...`

```bash
git add e2e/api
git commit -m "test(e2e): add contract-checked API smoke tests for checkout flow and auth"
```

---

### Task 4: API tests: cancellation, oversell and idempotency

**Files:**
- Create: `e2e/api/{cancel_test.go,oversell_test.go,idempotency_test.go}`

**Interfaces:**
- Consumes: the harness from Task 3 (`getWorld`, `newProduct`, `stockOf`, `newKey`, `ctx`, `actor`).

- [x] **Step 1: Write the cancel test**

`e2e/api/cancel_test.go`:

```go
package api_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// Success criterion 4: cancelling returns stock. Release is asynchronous
// (outbox → Kafka → catalog consumer); WS6 §3 allows up to 5 s.
func TestCancelReturnsStock(t *testing.T) {
	w := getWorld(t)
	buyer := w.buyers[1]
	require.NoError(t, buyer.ClearCart(ctx(t)))
	p := newProduct(t, w.sellerA, w.categoryID, "Nồi đất kho cá "+w.run, 5)
	sku := p.Skus[0]

	place := func(qty int) client.Order {
		_, err := buyer.SetCartItem(ctx(t), sku.ID, qty)
		require.NoError(t, err)
		res, err := buyer.Checkout(ctx(t), newKey(), client.CheckoutRequest{SkuIDs: []string{sku.ID}, ShippingAddress: client.DemoAddress("Người mua 1"), PaymentMethod: "COD"}, nil)
		require.NoError(t, err)
		return res.Orders[0]
	}

	// Buyer cancels a pending order.
	o := place(2)
	assert.Equal(t, 3, stockOf(t, p.ID, sku.ID))
	o, err := buyer.CancelOrder(ctx(t), o.ID, "Đổi ý")
	require.NoError(t, err)
	assert.Equal(t, "CANCELLED", o.Status)
	assert.Eventually(t, func() bool { return stockOf(t, p.ID, sku.ID) == 5 }, 5*time.Second, 200*time.Millisecond, "buyer cancel returns stock")

	// Seller cancels a confirmed order.
	o = place(1)
	_, err = w.sellerA.SellerOrderAction(ctx(t), o.ID, "confirm", "")
	require.NoError(t, err)
	_, err = buyer.CancelOrder(ctx(t), o.ID, "Muộn rồi")
	assert.True(t, client.IsCode(err, "INVALID_STATE_TRANSITION"), "buyer cannot cancel a confirmed order: %v", err)
	o, err = w.sellerA.SellerOrderAction(ctx(t), o.ID, "cancel", "Hết hàng mẫu")
	require.NoError(t, err)
	assert.Equal(t, "CANCELLED", o.Status)
	assert.Eventually(t, func() bool { return stockOf(t, p.ID, sku.ID) == 5 }, 5*time.Second, 200*time.Millisecond, "seller cancel returns stock")

	// Stock never overshoots: releasing is idempotent.
	time.Sleep(2 * time.Second)
	assert.Equal(t, 5, stockOf(t, p.ID, sku.ID))
}
```

- [x] **Step 2: Write the oversell test**

`e2e/api/oversell_test.go`:

```go
package api_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// Success criterion 5: five buyers race for two units; exactly two orders
// are placed, the rest get OUT_OF_STOCK, and stock ends at zero.
func TestConcurrentCheckoutsNeverOversell(t *testing.T) {
	w := getWorld(t)
	p := newProduct(t, w.sellerB, w.categoryID, "Ấm tử sa pha trà "+w.run, 2)
	sku := p.Skus[0]
	for _, b := range w.buyers {
		require.NoError(t, b.ClearCart(ctx(t)))
		_, err := b.SetCartItem(ctx(t), sku.ID, 1)
		require.NoError(t, err)
	}

	start := make(chan struct{})
	errs := make([]error, len(w.buyers))
	var wg sync.WaitGroup
	for i, b := range w.buyers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = b.Checkout(ctx(t), newKey(), client.CheckoutRequest{SkuIDs: []string{sku.ID}, ShippingAddress: client.DemoAddress("Người mua"), PaymentMethod: "COD"}, nil)
		}()
	}
	close(start)
	wg.Wait()

	ok, outOfStock := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case client.IsCode(err, "OUT_OF_STOCK"):
			outOfStock++
		default:
			t.Errorf("unexpected checkout error: %v", err)
		}
	}
	assert.Equal(t, 2, ok)
	assert.Equal(t, 3, outOfStock)
	assert.Equal(t, 0, stockOf(t, p.ID, sku.ID))

	// Releasing the failed checkouts' (non-existent) reservations must not add stock back.
	time.Sleep(3 * time.Second)
	assert.Equal(t, 0, stockOf(t, p.ID, sku.ID))
}
```

- [x] **Step 3: Write the idempotency tests**

`e2e/api/idempotency_test.go`:

```go
package api_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

func ordersOfCheckout(t *testing.T, b actor, checkoutID string) int {
	t.Helper()
	page, err := b.Orders(ctx(t), "")
	require.NoError(t, err)
	n := 0
	for _, o := range page.Items {
		if o.CheckoutID == checkoutID {
			n++
		}
	}
	return n
}

func TestCheckoutReplaysSameKeyAndRejectsReusedKey(t *testing.T) {
	w := getWorld(t)
	buyer := w.buyers[2]
	require.NoError(t, buyer.ClearCart(ctx(t)))
	pa := newProduct(t, w.sellerA, w.categoryID, "Khay tre đan "+w.run, 10)
	pb := newProduct(t, w.sellerB, w.categoryID, "Giỏ mây tre "+w.run, 10)
	for _, p := range []client.Product{pa, pb} {
		_, err := buyer.SetCartItem(ctx(t), p.Skus[0].ID, 1)
		require.NoError(t, err)
	}
	key := newKey()
	req := client.CheckoutRequest{ShippingAddress: client.DemoAddress("Người mua 2"), PaymentMethod: "COD"}

	first, err := buyer.Checkout(ctx(t), key, req, nil)
	require.NoError(t, err)
	second, err := buyer.Checkout(ctx(t), key, req, nil)
	require.NoError(t, err)
	assert.Equal(t, first.CheckoutID, second.CheckoutID)
	assert.ElementsMatch(t, []string{first.Orders[0].ID, first.Orders[1].ID}, []string{second.Orders[0].ID, second.Orders[1].ID})
	assert.Equal(t, 2, ordersOfCheckout(t, buyer, first.CheckoutID))
	assert.Equal(t, 9, stockOf(t, pa.ID, pa.Skus[0].ID), "replay reserves nothing")

	req.ShippingAddress.Line1 = "99 Đường Khác"
	_, err = buyer.Checkout(ctx(t), key, req, nil)
	assert.True(t, client.IsCode(err, "IDEMPOTENCY_KEY_REUSED"), "same key, different body: %v", err)
}

// Two concurrent submits with one key (a double click) create one set of orders.
func TestConcurrentDoubleSubmitCreatesOneCheckout(t *testing.T) {
	w := getWorld(t)
	buyer := w.buyers[3]
	require.NoError(t, buyer.ClearCart(ctx(t)))
	p := newProduct(t, w.sellerA, w.categoryID, "Chổi đót quét nhà "+w.run, 10)
	_, err := buyer.SetCartItem(ctx(t), p.Skus[0].ID, 1)
	require.NoError(t, err)
	key := newKey()
	req := client.CheckoutRequest{ShippingAddress: client.DemoAddress("Người mua 3"), PaymentMethod: "COD"}

	var wg sync.WaitGroup
	results := make([]client.CheckoutResponse, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results[i], errs[i] = buyer.Checkout(ctx(t), key, req, nil) }()
	}
	wg.Wait()

	checkouts := map[string]bool{}
	for i := range 2 {
		if errs[i] != nil {
			assert.Equal(t, http.StatusConflict, client.StatusOf(errs[i]), "the loser sees 'in progress': %v", errs[i])
			continue
		}
		checkouts[results[i].CheckoutID] = true
	}
	require.Len(t, checkouts, 1, "exactly one checkout id across both submits")
	page, err := buyer.Orders(ctx(t), "")
	require.NoError(t, err)
	n := 0
	for _, o := range page.Items {
		if len(o.Items) > 0 && o.Items[0].ProductID == p.ID {
			n++
		}
	}
	assert.Equal(t, 1, n)
	assert.Equal(t, 9, stockOf(t, p.ID, p.Skus[0].ID))
}
```

- [x] **Step 4: Run and triage**

Run: `cd e2e && GOWORK=off go test -count=1 -v -run 'TestCancelReturnsStock|TestConcurrentCheckouts|TestCheckoutReplays|TestConcurrentDoubleSubmit' ./api/`
Expected: PASS. Handle failures as in Task 3 Step 5. The WS3 known gaps apply: a Tx B race answers 409 `CONFLICT` instead of `OUT_OF_STOCK`, and orphaned idempotency rows exist (memory `ws3-order-followups`). If the oversell test hits the first one, record it as a finding against WS3 with the evidence. Don't loosen the assertion silently.

- [x] **Step 5: Stability check and commit**

Run: `cd e2e && GOWORK=off go test -count=3 -run 'TestConcurrentCheckouts|TestConcurrentDoubleSubmit' ./api/`
Expected: `ok` (3 iterations, no flakes).

```bash
git add e2e/api
git commit -m "test(e2e): cover cancel restock, oversell guard and checkout idempotency"
```

---

### Task 5: Observability: one checkout trace in Jaeger

**Files:**
- Create: `e2e/api/trace_test.go`

**Interfaces:**
- Consumes: the Task 3 harness and `jaegerURL`.

- [x] **Step 1: Write the test**

`e2e/api/trace_test.go`:

```go
package api_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

type jaegerTrace struct {
	Data []struct {
		Spans []struct {
			OperationName string `json:"operationName"`
			ProcessID     string `json:"processID"`
		} `json:"spans"`
		Processes map[string]struct {
			ServiceName string `json:"serviceName"`
		} `json:"processes"`
	} `json:"data"`
}

// Success criterion 6: a checkout started with a known traceparent produces
// one Jaeger trace with spans from gateway, order and catalog, including the
// ReserveStock RPC on catalog.
func TestCheckoutTraceCrossesGatewayOrderCatalog(t *testing.T) {
	w := getWorld(t)
	buyer := w.buyers[4]
	require.NoError(t, buyer.ClearCart(ctx(t)))
	p := newProduct(t, w.sellerA, w.categoryID, "Quạt nan tre "+w.run, 5)
	_, err := buyer.SetCartItem(ctx(t), p.Skus[0].ID, 1)
	require.NoError(t, err)

	traceID, spanID := randHex(16), randHex(8)
	_, err = buyer.Checkout(ctx(t), newKey(),
		client.CheckoutRequest{ShippingAddress: client.DemoAddress("Người mua 4"), PaymentMethod: "COD"},
		http.Header{"Traceparent": {"00-" + traceID + "-" + spanID + "-01"}})
	require.NoError(t, err)

	var services map[string]bool
	var reserveOnCatalog bool
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		services, reserveOnCatalog = fetchTrace(t, traceID)
		if services["gateway"] && services["order"] && services["catalog"] && reserveOnCatalog {
			return
		}
		time.Sleep(time.Second)
	}
	names := make([]string, 0, len(services))
	for s := range services {
		names = append(names, s)
	}
	sort.Strings(names)
	t.Fatalf("trace %s: services %v, catalog ReserveStock span found: %v (want gateway, order, catalog + ReserveStock)", traceID, names, reserveOnCatalog)
}

func fetchTrace(t *testing.T, traceID string) (map[string]bool, bool) {
	t.Helper()
	resp, err := http.Get(jaegerURL + "/api/traces/" + traceID)
	require.NoError(t, err)
	defer resp.Body.Close()
	services := map[string]bool{}
	if resp.StatusCode != http.StatusOK {
		return services, false
	}
	var tr jaegerTrace
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tr))
	reserve := false
	for _, d := range tr.Data {
		for _, pr := range d.Processes {
			services[pr.ServiceName] = true
		}
		for _, s := range d.Spans {
			if d.Processes[s.ProcessID].ServiceName == "catalog" && strings.Contains(s.OperationName, "ReserveStock") {
				reserve = true
			}
		}
	}
	return services, reserve
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [x] **Step 2: Run it**

Run: `cd e2e && GOWORK=off go test -count=1 -v -run TestCheckoutTrace ./api/`
Expected: PASS. If a service is missing from the trace, it's a finding: a missing propagator, an uninstrumented gRPC client or server, or the gateway not extracting the incoming context. Collect evidence with `curl -s localhost:16686/api/services` and `curl -s localhost:16686/api/traces/<id> | jq '.data[].processes'`. Root-cause it in the owning service's `otelx`/`grpcx` wiring and report it.

- [x] **Step 3: Run the whole API suite, then commit**

Run: `cd e2e && GOWORK=off go test -count=1 -timeout 15m ./api/`
Expected: `ok  github.com/ngoxuanchien/e-commerce/e2e/api`

```bash
git add e2e/api/trace_test.go
git commit -m "test(e2e): assert a checkout trace spans gateway, order and catalog in Jaeger"
```

---

### Task 6: Playwright project, auth spec and happy path

**Files:**
- Create: `e2e/playwright/{package.json,package-lock.json,tsconfig.json,playwright.config.ts}`, `e2e/playwright/support/{api.ts,ui.ts}`, `e2e/playwright/tests/{auth.spec.ts,happy-path.spec.ts}`

**Interfaces:**
- Produces (for Task 7):
  - In `support/api.ts`: `GATEWAY_URL`, `WEB_URL`, `PASSWORD`, `runId(): string`, `withRateLimitRetry(send)`, `class ApiUser` with `static register(fullName, email)`, `account: Account`, `openShop(name, slug)`, `createProduct(name, stock, price?)`, `product(id)`, `stock(productId)`, `setCartItem(skuId, qty)`, `orders()`, `status(path)`, `dispose()`; types `Account`, `Product`, `Order`.
  - In `support/ui.ts`: `signIn(page, account)`, `registerViaUi(page, account)`, `loginViaUi(page, account)`, `logout(page, fullName)`, `search(page, q)`, `addToCart(page, productName, variant?)`, `openCheckout(page)`, `placeOrderButton(page)`, `OUT_OF_STOCK_MESSAGE`.

- [ ] **Step 1: Scaffold the project**

`e2e/playwright/package.json`:

```json
{
  "name": "marketplace-e2e",
  "private": true,
  "version": "0.1.0",
  "scripts": {
    "test": "playwright test",
    "typecheck": "tsc --noEmit"
  },
  "devDependencies": {
    "@playwright/test": "^1.63.0",
    "@types/node": "^24.19.0",
    "typescript": "~5.9.3"
  },
  "engines": {
    "node": ">=24"
  }
}
```

`e2e/playwright/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2023",
    "module": "commonjs",
    "moduleResolution": "node",
    "strict": true,
    "noEmit": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "types": ["node"]
  },
  "include": ["playwright.config.ts", "support/**/*.ts", "tests/**/*.ts"]
}
```

`e2e/playwright/playwright.config.ts`:

```ts
import { defineConfig, devices } from '@playwright/test';
import { WEB_URL } from './support/api';

// The whole suite shares one real stack and the gateway's per-IP rate limits,
// so tests run one at a time.
export default defineConfig({
  testDir: 'tests',
  fullyParallel: false,
  workers: 1,
  timeout: 120_000,
  expect: { timeout: 10_000 },
  retries: process.env.CI ? 1 : 0,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : [['list']],
  use: {
    ...devices['Desktop Chrome'],
    baseURL: WEB_URL,
    locale: 'vi-VN',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
});
```

Run: `cd e2e/playwright && npm install && npx playwright install chromium`
Expected: `package-lock.json` is created and Chromium is installed.

- [ ] **Step 2: Write the support modules**

`e2e/playwright/support/api.ts`:

```ts
import { type APIRequestContext, type APIResponse, request } from '@playwright/test';

export const GATEWAY_URL = process.env.GATEWAY_URL ?? 'http://localhost:8080';
export const WEB_URL = process.env.WEB_URL ?? 'http://localhost:3000';
export const PASSWORD = 'matkhau123';

export interface Account {
  email: string;
  fullName: string;
}
export interface Sku {
  id: string;
  name: string;
  price: number;
  stock: number;
  active: boolean;
}
export interface Product {
  id: string;
  name: string;
  shop: { id: string; name: string; slug: string };
  skus: Sku[];
}
export interface Order {
  id: string;
  checkout_id: string;
  status: string;
  shop: { id: string; name: string };
}
interface Category {
  id: string;
  slug: string;
  children: Category[];
}

/** 8 lowercase letters: one search token, valid in shop slugs and emails. */
export function runId(): string {
  return Array.from({ length: 8 }, () => String.fromCharCode(97 + Math.floor(Math.random() * 26))).join('');
}

/**
 * The gateway allows 10 login/register calls per minute per client IP and
 * rejects with 429 before proxying, so waiting Retry-After and resending is
 * always safe.
 */
export async function withRateLimitRetry(send: () => Promise<APIResponse>): Promise<APIResponse> {
  const deadline = Date.now() + 180_000;
  for (;;) {
    const res = await send();
    if (res.status() !== 429) return res;
    const waitMs = Number(res.headers()['retry-after'] ?? '1') * 1000;
    if (Date.now() + waitMs > deadline) return res;
    await new Promise((resolve) => setTimeout(resolve, waitMs));
  }
}

/** A user driven through the gateway API, for test setup and assertions. */
export class ApiUser {
  private token = '';

  private constructor(
    private readonly ctx: APIRequestContext,
    readonly account: Account,
  ) {}

  static async register(fullName: string, email: string): Promise<ApiUser> {
    const user = new ApiUser(await request.newContext({ baseURL: GATEWAY_URL }), { email, fullName });
    await user.call('POST', '/api/auth/register', { email, password: PASSWORD, full_name: fullName });
    await user.login();
    return user;
  }

  async login(): Promise<void> {
    const res = await this.call<{ access_token: string }>('POST', '/api/auth/login', { email: this.account.email, password: PASSWORD });
    this.token = res.access_token;
  }

  async call<T = unknown>(method: string, path: string, data?: unknown, headers: Record<string, string> = {}): Promise<T> {
    const auth: Record<string, string> = this.token ? { Authorization: `Bearer ${this.token}` } : {};
    const res = await withRateLimitRetry(() => this.ctx.fetch(path, { method, data, headers: { ...auth, ...headers } }));
    if (!res.ok()) throw new Error(`${method} ${path} -> ${res.status()}: ${await res.text()}`);
    return (res.status() === 204 ? undefined : await res.json()) as T;
  }

  /** Opens a shop and refreshes the token so it carries the seller role. */
  async openShop(name: string, slug: string): Promise<void> {
    await this.call('POST', '/api/shops', { name, slug, description: 'Shop tạo bởi e2e.' });
    const res = await this.call<{ access_token: string }>('POST', '/api/auth/refresh');
    this.token = res.access_token;
  }

  async createProduct(name: string, stock: number, price = 120_000): Promise<Product> {
    const cats = await this.call<Category[]>('GET', '/api/categories');
    const cat = cats.flatMap((c) => c.children).find((c) => c.slug === 'do-dung-nha-bep');
    if (!cat) throw new Error('category do-dung-nha-bep not found');
    return this.call<Product>('POST', '/api/seller/products', {
      category_id: cat.id,
      name,
      description: `${name} — sản phẩm kiểm thử.`,
      status: 'active',
      image_keys: [],
      skus: [{ name: 'Tiêu chuẩn', price, stock, active: true }],
    });
  }

  product(id: string): Promise<Product> {
    return this.call<Product>('GET', `/api/products/${id}`);
  }

  async stock(productId: string): Promise<number> {
    return (await this.product(productId)).skus[0].stock;
  }

  async setCartItem(skuId: string, quantity: number): Promise<void> {
    await this.call('PUT', `/api/cart/items/${skuId}`, { quantity });
  }

  async orders(): Promise<Order[]> {
    return (await this.call<{ items: Order[] }>('GET', '/api/orders?limit=100')).items;
  }

  /** Status code of an authenticated GET, for asserting access denials. */
  async status(path: string): Promise<number> {
    const auth: Record<string, string> = this.token ? { Authorization: `Bearer ${this.token}` } : {};
    return (await this.ctx.fetch(path, { headers: auth })).status();
  }

  dispose(): Promise<void> {
    return this.ctx.dispose();
  }
}
```

`e2e/playwright/support/ui.ts`:

```ts
import { type Page, expect } from '@playwright/test';
import { type Account, GATEWAY_URL, PASSWORD, withRateLimitRetry } from './api';

export const OUT_OF_STOCK_MESSAGE = 'Một số sản phẩm không đủ hàng. Hãy giảm số lượng ở các dòng được đánh dấu rồi đặt lại.';

/**
 * Logs in without the form: the login call goes straight to the gateway, and
 * its HttpOnly refresh cookie (host "localhost", path /api/auth; cookies
 * ignore ports) is then sent by the web app on localhost:3000. The app
 * restores the session on load like a returning visitor. This keeps the web
 * origin's 10/min login bucket for the tests that exercise the form itself.
 */
export async function signIn(page: Page, account: Account): Promise<void> {
  const res = await withRateLimitRetry(() =>
    page.request.post(`${GATEWAY_URL}/api/auth/login`, { data: { email: account.email, password: PASSWORD } }),
  );
  expect(res.status(), await res.text()).toBe(200);
  await page.goto('/');
  await expect(page.getByRole('button', { name: account.fullName })).toBeVisible();
}

export async function registerViaUi(page: Page, account: Account): Promise<void> {
  await page.goto('/register');
  await page.getByLabel('Họ và tên').fill(account.fullName);
  await page.getByLabel('Email').fill(account.email);
  await page.getByLabel('Mật khẩu', { exact: true }).fill(PASSWORD);
  await page.getByLabel('Nhập lại mật khẩu').fill(PASSWORD);
  await page.getByRole('button', { name: 'Tạo tài khoản' }).click();
  await expect(page.getByText('Tạo tài khoản thành công. Mời bạn đăng nhập.')).toBeVisible();
}

export async function loginViaUi(page: Page, account: Account): Promise<void> {
  if (!page.url().includes('/login')) await page.goto('/login');
  await page.getByLabel('Email').fill(account.email);
  await page.getByLabel('Mật khẩu').fill(PASSWORD);
  await page.getByRole('button', { name: 'Đăng nhập' }).click();
  await expect(page.getByRole('button', { name: account.fullName })).toBeVisible();
}

export async function logout(page: Page, fullName: string): Promise<void> {
  await page.getByRole('button', { name: fullName }).click();
  await page.getByRole('button', { name: 'Đăng xuất' }).click();
  await expect(page.getByRole('link', { name: 'Đăng nhập' })).toBeVisible();
}

export async function search(page: Page, q: string): Promise<void> {
  const box = page.getByRole('searchbox', { name: 'Tìm sản phẩm' });
  await box.fill(q);
  await box.press('Enter');
  await expect(page).toHaveURL(/\/search\?q=/);
}

export async function addToCart(page: Page, productName: string, variant?: string): Promise<void> {
  await page.getByRole('link', { name: productName }).first().click();
  await expect(page.getByRole('heading', { level: 1, name: productName })).toBeVisible();
  if (variant) await page.getByRole('radio', { name: variant, exact: true }).check();
  await page.getByRole('button', { name: 'Thêm vào giỏ hàng' }).click();
  await expect(page.getByText('Đã thêm vào giỏ hàng').first()).toBeVisible();
}

/** Goes from the cart to the checkout page and fills the shipping address. */
export async function openCheckout(page: Page): Promise<void> {
  await page.getByRole('link', { name: /Giỏ hàng/ }).click();
  await page.getByRole('button', { name: 'Mua hàng' }).click();
  await page.getByLabel('Số điện thoại').fill('0901234567');
  await page.getByLabel('Số nhà, tên đường').fill('12 Lý Tự Trọng');
  await page.getByLabel('Phường/Xã').fill('Bến Nghé');
  await page.getByLabel('Quận/Huyện').fill('Quận 1');
  await page.getByLabel('Tỉnh/Thành phố').fill('TP. Hồ Chí Minh');
}

export function placeOrderButton(page: Page) {
  return page.getByRole('button', { name: 'Đặt hàng' });
}
```

Before running, verify every UI string used here and in the specs by grepping `web/src`. Example: `grep -rn "Đã nhận được hàng\|Hoàn thành\|Thêm phân loại\|Lý do huỷ" web/src`. The strings above come from `web/e2e/helpers.ts` and `web/src` as of 982c77c.

- [ ] **Step 3: Write the auth spec**

`e2e/playwright/tests/auth.spec.ts`:

```ts
import { expect, test } from '@playwright/test';
import { ApiUser, runId } from '../support/api';
import { loginViaUi, logout, signIn } from '../support/ui';

test('the session survives a reload on the real gateway; after logout /orders redirects to login', async ({ page, context }) => {
  const run = runId();
  const user = await ApiUser.register(`Khách ${run}`, `e2e-${run}-auth@example.test`);

  await loginViaUi(page, user.account);
  const cookie = (await context.cookies()).find((c) => c.name === 'refresh_token');
  expect(cookie).toMatchObject({ httpOnly: true, path: '/api/auth' });

  const refreshed = page.waitForResponse((r) => r.url().endsWith('/api/auth/refresh') && r.status() === 200);
  await page.reload();
  await refreshed;
  await expect(page.getByRole('button', { name: user.account.fullName })).toBeVisible();

  await logout(page, user.account.fullName);
  await page.reload();
  await expect(page.getByRole('link', { name: 'Đăng nhập' })).toBeVisible();
  await page.goto('/orders');
  await expect(page).toHaveURL(/\/login\?next=%2Forders$/);
  await user.dispose();
});

test('a buyer cannot open the seller center', async ({ page }) => {
  const run = runId();
  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);

  await signIn(page, buyer.account);
  await page.goto('/seller/products');
  await expect(page.getByRole('heading', { name: 'Bạn chưa có shop' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Thêm sản phẩm', exact: true })).toHaveCount(0);
  expect(await buyer.status('/api/seller/products')).toBe(403);
  await buyer.dispose();
});
```

- [ ] **Step 4: Write the happy path spec**

`e2e/playwright/tests/happy-path.spec.ts`:

```ts
import path from 'node:path';
import { expect, test } from '@playwright/test';
import { ApiUser, runId } from '../support/api';
import { addToCart, loginViaUi, logout, openCheckout, placeOrderButton, registerViaUi, search, signIn } from '../support/ui';

test('seller opens a shop and lists a product; buyer buys from two shops; the order completes', async ({ page }) => {
  test.setTimeout(240_000);
  const run = runId();
  const seller = { email: `e2e-${run}-seller@example.test`, fullName: `Người bán ${run}` };
  const shopName = `Gốm Sứ ${run}`;
  const productName = `Bình gốm men lam ${run}`;

  // A second shop and a buyer, prepared through the API.
  const sellerB = await ApiUser.register(`Chủ shop ${run}`, `e2e-${run}-seller-b@example.test`);
  await sellerB.openShop(`Đèn Gỗ ${run}`, `e2e-${run}-b`);
  const productB = await sellerB.createProduct(`Đèn ngủ gỗ thông ${run}`, 5);
  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);

  // 1. A new user registers and opens a shop, becoming a seller.
  await registerViaUi(page, seller);
  await loginViaUi(page, seller);
  await page.goto('/seller');
  await page.getByRole('link', { name: 'Mở shop của bạn' }).click();
  await page.getByLabel('Tên shop').fill(shopName);
  await expect(page.getByLabel('Địa chỉ shop')).toHaveValue(`gom-su-${run}`);
  await page.getByLabel('Giới thiệu shop').fill('Gốm men lam thủ công.');
  await page.getByRole('button', { name: 'Mở shop' }).click();
  await expect(page).toHaveURL(/\/seller\/products$/);

  // 2. The seller lists a product with a real image upload and two variants.
  await page.getByRole('link', { name: 'Thêm sản phẩm', exact: true }).click();
  await page.getByLabel('Danh mục').selectOption({ label: 'Trang trí nhà cửa' });
  await page.getByLabel('Tên sản phẩm').fill(productName);
  await page.getByLabel('Mô tả').fill('Men lam vẽ tay, cao 30 cm.');
  await page.getByLabel('Thêm ảnh').setInputFiles(path.join(__dirname, '..', '..', 'fixtures', 'images', 'home-03.jpg'));
  await expect(page.getByRole('img', { name: 'Ảnh 1' })).toBeVisible();
  await page.getByLabel('Tên phân loại 1').fill('Bộ nhỏ');
  await page.getByLabel('Giá (₫) phân loại 1').fill('250000');
  await page.getByLabel('Tồn kho phân loại 1').fill('5');
  await page.getByRole('button', { name: 'Thêm phân loại' }).click();
  await page.getByLabel('Tên phân loại 2').fill('Bộ lớn');
  await page.getByLabel('Giá (₫) phân loại 2').fill('390000');
  await page.getByLabel('Tồn kho phân loại 2').fill('3');
  await page.getByRole('button', { name: 'Tạo sản phẩm' }).click();
  await expect(page).toHaveURL(/\/seller\/products$/);
  await expect(page.getByRole('row', { name: new RegExp(productName) })).toBeVisible();
  await logout(page, seller.fullName);

  // 3. The buyer searches without diacritics and sees the uploaded image.
  await signIn(page, buyer.account);
  await search(page, `binh gom men lam ${run}`);
  const card = page.getByRole('listitem').filter({ hasText: productName });
  await expect(card.locator('img')).toHaveAttribute('src', /\/product-images\/shops\//);
  await expect.poll(() => card.locator('img').evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);
  await addToCart(page, productName, 'Bộ lớn');
  await search(page, `den ngu go thong ${run}`);
  await addToCart(page, productB.name);

  // 4. Checkout splits the cart into one COD order per shop.
  await page.getByRole('link', { name: /Giỏ hàng/ }).click();
  await expect(page.getByRole('region', { name: `Shop ${shopName}` })).toBeVisible();
  await expect(page.getByRole('region', { name: `Shop Đèn Gỗ ${run}` })).toBeVisible();
  await openCheckout(page);
  await placeOrderButton(page).click();
  await expect(page).toHaveURL(/\/orders\?placed=2$/);
  await expect(page.getByRole('article', { name: `Đơn hàng của ${shopName}` })).toBeVisible();
  await expect(page.getByRole('article', { name: `Đơn hàng của Đèn Gỗ ${run}` })).toBeVisible();
  await logout(page, buyer.account.fullName);

  // 5. The seller confirms, ships and delivers.
  await signIn(page, seller);
  await page.goto('/seller/orders');
  const order = page.getByRole('article', { name: `Đơn hàng của ${shopName}` });
  await order.getByRole('button', { name: 'Xác nhận đơn' }).click();
  await expect(order.locator('[data-status]')).toHaveText('Đã xác nhận');
  await order.getByRole('button', { name: 'Bắt đầu giao' }).click();
  await expect(order.locator('[data-status]')).toHaveText('Đang giao');
  await order.getByRole('button', { name: 'Xác nhận đã giao' }).click();
  await expect(order.locator('[data-status]')).toHaveText('Đã giao');
  await logout(page, seller.fullName);

  // 6. The buyer confirms receipt.
  await signIn(page, buyer.account);
  await page.goto('/orders');
  await page.getByRole('article', { name: `Đơn hàng của ${shopName}` }).getByRole('link', { name: 'Xem chi tiết' }).click();
  await page.getByRole('button', { name: 'Đã nhận được hàng' }).click();
  await expect(page.locator('[data-status]').first()).toHaveText('Hoàn thành');

  await Promise.all([sellerB.dispose(), buyer.dispose()]);
});
```

The seller tab in `/seller/orders` might filter to "Chờ xác nhận" and hide an order after it's confirmed. Check `web/src/features/seller-orders` and follow the tabs the way the web's own `web/e2e/seller.spec.ts` does. The web spec passes with the article staying visible across actions. If the real app behaves differently, adapt the navigation, not the assertions about status.

- [ ] **Step 5: Run the specs against the stack**

Run: `cd e2e/playwright && npx tsc --noEmit && npx playwright test tests/auth.spec.ts tests/happy-path.spec.ts`
Expected: `3 passed`.

Triage failures with Global Constraints' Findings protocol. Likely real-stack issues:
- a MinIO CORS rejection of the browser's presigned POST from `localhost:3000` (owner: WS0 `deploy/`, or WS2 if the bucket policy is at fault);
- the session restore (memory `ws5-contract-followups`: only stub-verified so far);
- image URLs not reachable from the browser.

For each one, reproduce it with a minimal failing check (Playwright trace plus a `curl` of the same request), record it, and report it.

- [ ] **Step 6: Commit**

```bash
git add e2e/playwright
git commit -m "test(e2e): add Playwright auth and happy-path specs against the real stack"
```

---

### Task 7: Playwright cancel, oversell and idempotency specs

**Files:**
- Create: `e2e/playwright/tests/{cancel-restock.spec.ts,oversell.spec.ts,idempotency.spec.ts}`

**Interfaces:**
- Consumes: `support/api.ts` and `support/ui.ts` from Task 6.

- [ ] **Step 1: Write the cancel spec**

`e2e/playwright/tests/cancel-restock.spec.ts`:

```ts
import { expect, test } from '@playwright/test';
import { ApiUser, runId } from '../support/api';
import { openCheckout, placeOrderButton, signIn } from '../support/ui';

test('a buyer cancels a pending order and the stock returns within 5 s', async ({ page }) => {
  const run = runId();
  const shopName = `Kho Hàng ${run}`;
  const seller = await ApiUser.register(`Chủ shop ${run}`, `e2e-${run}-seller@example.test`);
  await seller.openShop(shopName, `e2e-${run}-shop`);
  const product = await seller.createProduct(`Nồi đất kho cá ${run}`, 5);
  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);

  await signIn(page, buyer.account);
  await page.goto(`/products/${product.id}`);
  await page.getByRole('button', { name: 'Thêm vào giỏ hàng' }).click();
  await expect(page.getByText('Đã thêm vào giỏ hàng').first()).toBeVisible();
  await openCheckout(page);
  await placeOrderButton(page).click();
  await expect(page).toHaveURL(/\/orders\?placed=1$/);
  expect(await seller.stock(product.id)).toBe(4);

  await page.getByRole('article', { name: `Đơn hàng của ${shopName}` }).getByRole('link', { name: 'Xem chi tiết' }).click();
  await page.getByRole('button', { name: 'Huỷ đơn hàng' }).click();
  await page.getByLabel('Lý do huỷ').fill('Đặt nhầm sản phẩm');
  await page.getByRole('button', { name: 'Xác nhận huỷ' }).click();
  await expect(page.getByText('Lý do huỷ: Đặt nhầm sản phẩm')).toBeVisible();

  await expect.poll(() => seller.stock(product.id), { timeout: 5_000, intervals: [200] }).toBe(5);
  await Promise.all([seller.dispose(), buyer.dispose()]);
});
```

- [ ] **Step 2: Write the oversell spec**

`e2e/playwright/tests/oversell.spec.ts`:

```ts
import { type Page, expect, test } from '@playwright/test';
import { ApiUser, WEB_URL, runId } from '../support/api';
import { OUT_OF_STOCK_MESSAGE, openCheckout, placeOrderButton, signIn } from '../support/ui';

test('two buyers race for the last unit: exactly one order is placed', async ({ browser }) => {
  const run = runId();
  const seller = await ApiUser.register(`Chủ shop ${run}`, `e2e-${run}-seller@example.test`);
  await seller.openShop(`Hàng Hiếm ${run}`, `e2e-${run}-shop`);
  const product = await seller.createProduct(`Tranh sơn mài độc bản ${run}`, 1);

  const buyers: ApiUser[] = [];
  for (const i of [1, 2]) buyers.push(await ApiUser.register(`Người mua ${i} ${run}`, `e2e-${run}-buyer${i}@example.test`));

  const pages: Page[] = [];
  for (const b of buyers) {
    await b.setCartItem(product.skus[0].id, 1);
    const context = await browser.newContext({ baseURL: WEB_URL, locale: 'vi-VN' });
    const page = await context.newPage();
    await signIn(page, b.account);
    await openCheckout(page);
    pages.push(page);
  }

  await Promise.all(pages.map((p) => placeOrderButton(p).click()));
  await Promise.all(pages.map((p) => p.waitForURL(/\/orders\?placed=1$|\/cart$/)));

  const winners = pages.filter((p) => /\/orders\?placed=1$/.test(p.url()));
  const losers = pages.filter((p) => /\/cart$/.test(p.url()));
  expect(winners).toHaveLength(1);
  expect(losers).toHaveLength(1);
  await expect(losers[0].getByText(OUT_OF_STOCK_MESSAGE)).toBeVisible();
  expect(await seller.stock(product.id)).toBe(0);

  await Promise.all(pages.map((p) => p.context().close()));
  await Promise.all([seller.dispose(), ...buyers.map((b) => b.dispose())]);
});
```

- [ ] **Step 3: Write the idempotency spec**

`e2e/playwright/tests/idempotency.spec.ts`:

```ts
import { type Page, expect, test } from '@playwright/test';
import { ApiUser, runId } from '../support/api';
import { openCheckout, placeOrderButton, signIn } from '../support/ui';

async function setup(page: Page) {
  const run = runId();
  const seller = await ApiUser.register(`Chủ shop ${run}`, `e2e-${run}-seller@example.test`);
  await seller.openShop(`Tạp Hóa ${run}`, `e2e-${run}-shop`);
  const product = await seller.createProduct(`Mứt gừng Huế ${run}`, 5);
  const buyer = await ApiUser.register(`Người mua ${run}`, `e2e-${run}-buyer@example.test`);
  await buyer.setCartItem(product.skus[0].id, 1);
  await signIn(page, buyer.account);
  await openCheckout(page);

  const keys: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().endsWith('/api/checkout')) keys.push(r.headers()['idempotency-key']);
  });
  return { seller, buyer, product, keys };
}

test('double-clicking place order creates one set of orders', async ({ page }) => {
  const { seller, buyer, product, keys } = await setup(page);

  await placeOrderButton(page).dblclick();
  await expect(page).toHaveURL(/\/orders\?placed=1$/);

  expect(new Set(keys).size).toBe(1);
  expect(await buyer.orders()).toHaveLength(1);
  expect(await seller.stock(product.id)).toBe(4);
  await Promise.all([seller.dispose(), buyer.dispose()]);
});

test('a checkout whose response is lost is retried with the same key and not duplicated', async ({ page }) => {
  const { seller, buyer, product, keys } = await setup(page);

  let first = true;
  await page.route('**/api/checkout', async (route) => {
    if (!first) return route.continue();
    first = false;
    await route.fetch(); // the server commits the checkout …
    await route.abort('connectionreset'); // … but the browser never sees the answer
  });

  await placeOrderButton(page).click();
  await expect(placeOrderButton(page)).toBeEnabled();
  await expect(page).toHaveURL(/\/checkout/);
  await placeOrderButton(page).click();
  await expect(page).toHaveURL(/\/orders\?placed=1$/);

  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  expect(await buyer.orders()).toHaveLength(1);
  expect(await seller.stock(product.id)).toBe(4);
  await Promise.all([seller.dispose(), buyer.dispose()]);
});
```

- [ ] **Step 4: Run the full Playwright suite twice**

Run: `cd e2e/playwright && npx tsc --noEmit && npx playwright test && npx playwright test`
Expected: `8 passed` both times.

- [ ] **Step 5: Commit**

```bash
git add e2e/playwright/tests
git commit -m "test(e2e): add Playwright cancel-restock, oversell and idempotency specs"
```

---

### Task 8: `make e2e`, e2e README and the CI job

**Files:**
- Modify: `Makefile` (the `e2e` target only)
- Modify: `.github/workflows/ci.yml` (add the `e2e` job only)
- Create: `e2e/README.md`

- [ ] **Step 1: Replace the `e2e` target**

```make
e2e: ## Run API smoke tests + Playwright against a running stack (make up-app seed first)
	cd e2e && GOWORK=off go test -count=1 -timeout 20m ./...
	cd e2e/playwright && npm ci --no-audit --no-fund && npx playwright install chromium && npx playwright test
```

`./...` also runs the client and seed unit tests, which are fast.

Run: `make e2e`
Expected: the Go packages print `ok`, then `8 passed`. Exit 0.

- [ ] **Step 2: Add the CI job**

Append to `.github/workflows/ci.yml` under `jobs:`:

```yaml
  e2e:
    runs-on: ubuntu-latest
    timeout-minutes: 45
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: e2e/go.mod
          cache-dependency-path: '**/go.sum'
      - uses: actions/setup-node@v5
        with:
          node-version: 24
          cache: npm
          cache-dependency-path: e2e/playwright/package-lock.json
      - uses: actions/cache@v4
        with:
          path: bin
          key: tools-${{ runner.os }}-${{ hashFiles('Makefile') }}
      - run: make tools
      - name: lint e2e module
        working-directory: e2e
        run: GOWORK=off $GITHUB_WORKSPACE/bin/golangci-lint run ./...
      - name: install Playwright browser
        working-directory: e2e/playwright
        run: npm ci --no-audit --no-fund && npx playwright install --with-deps chromium
      - name: start the stack
        run: make keys up-app
      - run: make seed
      - run: make e2e
        env:
          CI: 'true'
      - name: collect service logs
        if: failure()
        run: docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.web.yml --profile app logs --no-color > e2e-service-logs.txt
      - uses: actions/upload-artifact@v4
        if: failure()
        with:
          name: e2e-artifacts
          path: |
            e2e/playwright/playwright-report
            e2e/playwright/test-results
            e2e-service-logs.txt
          retention-days: 7
```

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml')); print('yaml ok')"`
Expected: `yaml ok`.

- [ ] **Step 3: Write `e2e/README.md`**

Contents:
- What lives here: the client, seed, API tests, Playwright specs and fixtures.
- Prerequisites: a running stack (`make up-app`), Go 1.27.1 and Node 24.
- The commands `make seed` / `make e2e`, and how to run one Go test (`cd e2e && GOWORK=off go test -run TestCancelReturnsStock ./api/`) or one spec (`cd e2e/playwright && npx playwright test tests/oversell.spec.ts`).
- The env vars `GATEWAY_URL`, `WEB_URL`, `JAEGER_URL` and `E2E_FINDINGS`.
- Why `GOWORK=off`.
- Why tests are slow after `make seed`: 429 back-off on the 10/min login bucket.
- How to open a Playwright trace (`npx playwright show-trace test-results/…/trace.zip`).
- The table mapping the six P1 success criteria to tests (the same as Task 10 Step 2).

- [ ] **Step 4: Commit**

```bash
git add Makefile .github/workflows/ci.yml e2e/README.md
git commit -m "ci: run seed, API smoke tests and Playwright against the compose stack"
```

---

### Task 9: Documentation: README, architecture and ADRs

**Files:**
- Create/Modify: `README.md`, `docs/architecture.md`, `docs/adr/0001-microservices-database-per-service.md`, `docs/adr/0002-grpc-internal-rest-external.md`, `docs/adr/0003-transactional-outbox-kafka.md`, `docs/adr/0004-stock-reservation-strategy.md`, `docs/adr/0005-jwt-rs256-refresh-rotation.md`

Every statement must match the code on this branch. Check each fact against the specs, the service READMEs (`services/catalog/README.md`, `services/order/README.md`), `deploy/docker-compose.yml` and the Makefile. Don't copy it from memory. Use Mermaid for diagrams, since GitHub renders it.

- [ ] **Step 1: `README.md`**, in this order:
  1. The title and a one-paragraph pitch: a Shopee-like multi-vendor marketplace, Go microservices + React, built as a portfolio project; P1 covers core buying and selling with COD.
  2. A feature list covering the six P1 success criteria.
  3. An architecture diagram: a Mermaid `flowchart LR` of browser → web → gateway → identity/catalog/order, with Postgres DBs, Redis, Kafka `order.events`, MinIO and Jaeger. Link to `docs/architecture.md`.
  4. A tech-choices table: Go 1.27, gRPC + buf, sqlc + pgx, golang-migrate, franz-go, OpenTelemetry → Jaeger, React + Vite + TanStack Query + Tailwind, Playwright, testcontainers. Verify each against the `go.mod` files and `web/package.json`.
  5. **Quick start**, timed at under 10 minutes: prerequisites (Docker with Compose v2, Go 1.27, Node 24, openssl, make); `git clone …`, `make keys up-app seed`; then open http://localhost:3000.
  6. The demo accounts table from the seed output. The password is `matkhau123`.
  7. A URLs table: web :3000, gateway :8080, Jaeger UI :16686, MinIO console :9001 (`minioadmin`/`minioadmin`).
  8. A demo script: search "ao thun", buy from two shops, log in as `seller1@example.test` to confirm and ship, then view the trace in Jaeger by searching service `gateway`.
  9. Running tests: `make test`, `make lint`, `make e2e`, and web `npm test` / `npm run e2e`.
  10. A repository layout table (who owns what) and the roadmap (P2–P5 from the overview).
  11. Troubleshooting:
      - A port already in use.
      - A service failing on `no migration found for version N`: the shared `pgdata` volume holds newer migrations from another branch. Check out main or reset volumes with `make down && docker volume rm marketplace_pgdata`, which deletes data.
      - 429 during tests.

- [ ] **Step 2: `docs/architecture.md`**, with these sections:
  - Services and ownership: a table with port, database, responsibilities, sync/async interfaces and the owning workstream.
  - Request path through the gateway: header stripping and injection, auth levels, rate limits.
  - Checkout sequence (Mermaid `sequenceDiagram`): buyer → gateway → order (idempotency row → GetSkus → Tx A PENDING orders → ReserveStock per order → Tx C PENDING_CONFIRMATION + OrderCreated outbox → cart cleanup). Include the failure branch (`alt` any reservation fails → ReleaseStock all → Tx B CANCELLED → 409).
  - Cancellation sequence: buyer/seller → order (transition + OrderStatusChanged + OrderCancelled in the outbox, same tx) → relay → Kafka → catalog consumer (processed_events dedupe + release in one tx).
  - The consistency approach, one subsection each: stock reservation with a conditional `UPDATE … stock >= q`, sorted SKU order and idempotency per order_id; the transactional outbox with at-least-once delivery and idempotent consumers; the checkout idempotency key; the sweeper for stuck `PENDING` orders; optimistic locking on order transitions.
  - Observability: OTLP → Jaeger, and trace propagation over HTTP, gRPC and Kafka headers.
  - Known limitations: the WS2/WS3/WS4 follow-ups that are still open, plus the WS6 Findings.

- [ ] **Step 3: The five ADRs**, each ≤ 1 page with Status / Context / Decision / Consequences. Status is "Accepted", dated 2026-09-28.
  - 0001: microservices + database per service. Context: a portfolio goal of showcasing service boundaries. Consequences: no cross-DB joins, snapshots in orders, eventual consistency for stock release.
  - 0002: gRPC internally, REST externally. Contracts in `proto/` (buf lint/breaking) and `api/openapi` (source of truth for the web types); the gateway translates nothing, it only proxies REST.
  - 0003: transactional outbox with Kafka. Covers the dual-write problem, `pkg/outbox` relay with `SKIP LOCKED`, at-least-once delivery, `processed_events` dedupe, and `traceparent` in headers.
  - 0004: stock reservation strategy. Covers synchronous reserve at checkout (vs. reserve-on-confirm or optimistic cart), the conditional update without locks held across services, all-or-nothing per checkout with compensation, the sweeper, and the async release on cancel. Also the known limitation that a seller stock PATCH overwrites on-hand stock (memory `ws2-catalog-followups`).
  - 0005: JWT RS256 + refresh rotation. Covers the JWKS-verifying gateway, 15 min access tokens held only in memory, and HttpOnly refresh cookies rotated per use with family revocation on reuse. The role change after opening a shop needs `/refresh`.

- [ ] **Step 4: Check the docs**

Run: `grep -rn "TODO\|TBD" README.md docs/architecture.md docs/adr/`
Expected: no output.

Check every relative link: `grep -oE '\]\(([^)#]+)' README.md docs/architecture.md docs/adr/*.md | sed 's/.*](//' | grep -v '^http' | sort -u`. Resolve each path against its file's directory and confirm it exists.

Check the Mermaid syntax by pasting each block into `npx --yes @mermaid-js/mermaid-cli`. Example: `npx --yes @mermaid-js/mermaid-cli -i docs/architecture.md -o "$SCRATCH/arch.md"`, where `$SCRATCH` is the session scratchpad; it fails on syntax errors.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/architecture.md docs/adr
git commit -m "docs: add README, architecture overview and ADRs 0001-0005"
```

---

### Task 10: Acceptance verification and findings report

**Files:**
- Modify: this plan (the Findings table and ticks), plus `e2e/README.md` if the criteria mapping changes

- [ ] **Step 1: Fresh-clone run**

**Ask the user first.** This step recreates the shared compose stack (project `marketplace`) and, for a truly fresh run, deletes its volumes, which removes all local data for every worktree. Once they confirm:

```bash
SCRATCH=/private/tmp/claude-501/-Users-xuanchien-Project-claude-app-E-Commerce/6ff6a123-1406-4d70-b999-b07441d1f007/scratchpad
git clone --branch ws6-integration /Users/xuanchien/Project/claude-app/E-Commerce-ws6-integration "$SCRATCH/fresh" && cd "$SCRATCH/fresh"
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.web.yml --profile app down -v --remove-orphans   # only with user approval
time make keys up-app seed
time make e2e
```

Expected: every command exits 0. The first `time` shows a stack start plus seed well inside the 10-minute README budget (image builds dominate). Capture the output for the report.

- [ ] **Step 2: Map the six P1 success criteria to passing tests** and paste the command output into the report:

| # | Criterion | Automated by |
|---|---|---|
| 1 | Register/login, browse, search with/without diacritics, details, cart from several shops | `api/flow_test.go` `TestCheckoutAcrossTwoShopsAndLifecycle`; `playwright/tests/happy-path.spec.ts`, `auth.spec.ts` |
| 2 | Checkout splits into one COD order per shop | same two tests |
| 3 | Open shop → seller → products with SKUs and images | `happy-path.spec.ts` (UI upload, 2 variants); `flow_test.go` (presigned upload) |
| 4 | Confirm → ship → deliver → complete; cancel returns stock | `flow_test.go`, `happy-path.spec.ts`, `api/cancel_test.go`, `cancel-restock.spec.ts` |
| 5 | No oversell under concurrency | `api/oversell_test.go`, `oversell.spec.ts`, plus idempotency tests |
| 6 | Tests pass in every service; checkout traceable gateway → order → catalog | CI `go` matrix + `web` job; `api/trace_test.go` |

- [ ] **Step 3: README timing.** Follow README Quick start literally from the fresh clone and time it: clone → `make keys up-app seed` → log in at :3000 as `buyer1@example.test`. It must take under 10 minutes on this machine with a warm Docker layer cache, and the README must state the cold-cache expectation. Fix the README if a step is missing.

- [ ] **Step 4: Lint and type checks**

Run: `cd e2e && ../bin/golangci-lint run ./... && GOWORK=off go vet ./... && cd playwright && npx tsc --noEmit`
Expected: clean.

- [ ] **Step 5: Findings report.** For every row in the Findings table, confirm there's a skipped reproduction test that fails with `E2E_FINDINGS=1`. Report each finding to the user with its owner, root-cause evidence and suggested fix. Also report the CI status: the e2e job can only be proven green after pushing, so say so explicitly if it hasn't run.

- [ ] **Step 6: Commit, then finish the branch**

```bash
git add docs/superpowers/plans/2026-09-28-ws6-integration.md e2e/README.md
git commit -m "docs: record WS6 acceptance evidence and findings"
```

Then use superpowers:finishing-a-development-branch.
