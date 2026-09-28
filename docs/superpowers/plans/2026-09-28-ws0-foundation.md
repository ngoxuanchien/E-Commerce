# WS0 Foundation & Contracts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the repo layout, all contracts (OpenAPI, proto, event schemas), the shared `pkg/` libraries, four runnable service skeletons, local Docker Compose infrastructure and CI so WS1–WS5 can proceed in parallel.

**Architecture:** A Go workspace (`go.work`) of six modules — `pkg`, `gen`, and one per service — where each service `go.mod` also carries `replace` directives to `../../pkg` and `../../gen` so it builds with `GOWORK=off` (Docker, `go mod tidy`). Contracts live in `api/` and `proto/` and are validated by `redocly`, `buf` and a kin-openapi loader test. Everything is driven by the `Makefile`, which pins every tool version into `./bin`; CI calls the same Makefile targets.

**Tech Stack:** Go 1.27.1, net/http ServeMux, pgx v5 + golang-migrate, franz-go, OpenTelemetry (OTLP gRPC), gRPC + buf, testcontainers-go, kin-openapi, santhosh-tekuri/jsonschema, Docker Compose (Postgres 17, Redis 7, Kafka 4.3 KRaft, MinIO, Jaeger 2), GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-28-marketplace-p1-ws0-foundation.md` (umbrella: `docs/superpowers/specs/2026-09-28-marketplace-p1-overview.md`).

## Global Constraints

- Module root: `github.com/ngoxuanchien/e-commerce/<dir>`; every `go.mod` says `go 1.27.1`.
- Tool versions (pinned in `Makefile`): buf `v1.73.0`, sqlc `v1.31.1`, golangci-lint `v2.14.0`, protoc-gen-go `v1.36.12`, protoc-gen-go-grpc `v1.6.2`, @redocly/cli `2.54.3`.
- IDs UUIDv7 strings; timestamps UTC RFC 3339; money `int64` VND; JSON `snake_case`.
- Pagination: `?limit=` default 20, max 100, `?cursor=` opaque; response `{ "items": [...], "next_cursor": string|null }`.
- Error body: `{"error":{"code","message","details"}}`; codes and statuses exactly: `VALIDATION_ERROR` 400 · `UNAUTHENTICATED` 401 · `FORBIDDEN` 403 · `NOT_FOUND` 404 · `CONFLICT` 409 · `OUT_OF_STOCK` 409 · `INVALID_STATE_TRANSITION` 409 · `IDEMPOTENCY_KEY_REUSED` 422 · `RATE_LIMITED` 429 · `INTERNAL` 500 · `UPSTREAM_UNAVAILABLE` 503.
- Internal headers: `X-User-Id`, `X-User-Roles` (comma-separated), `X-Request-Id`, `traceparent`.
- Ports: gateway 8080; identity 8081/9081; catalog 8082/9082; order 8083/9083 (HTTP/gRPC).
- Env: `SERVICE_NAME`, `HTTP_ADDR`, `GRPC_ADDR`, `DATABASE_URL`, `REDIS_URL`, `KAFKA_BROKERS`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `LOG_LEVEL`.
- Kafka topic `order.events`, 3 partitions, key = `order_id`; envelope `{event_id, event_type, version, occurred_at, data}`.
- Only gateway, web, Jaeger UI and MinIO (API + console) publish host ports.
- All code, comments, docs and commit messages in English.

## Review Focus

1. **Malicious `X-Request-Id`** (spaces, newlines, 10 KB): must be replaced by a generated ID, never echoed or logged verbatim → test in Task 3.
2. **Messy `X-User-Roles`** (`" buyer, seller,,"`): parsed into `[buyer seller]`; an empty `X-User-Id` means anonymous even when roles are present → test in Task 4.
3. **Malformed request bodies** (trailing data, unknown fields, >1 MB, empty): always 400 `VALIDATION_ERROR`, never 500 → test in Task 2.
4. **Kafka handler keeps failing**: message is retried and its offset is not committed, so a restarted consumer sees it again → test in Task 11.
5. **Kafka down while relaying the outbox**: rows stay unpublished and are sent on a later batch (no loss) → test in Task 12.

---

## File Structure

```
go.work                                   workspace: pkg, gen, 4 services
go.work.sum                               committed (removed from .gitignore)
.editorconfig  .golangci.yml  .dockerignore  Makefile
.github/workflows/ci.yml
api/redocly.yaml
api/openapi/{common,identity,catalog,order}.yaml
api/events/{envelope,order-created,order-status-changed,order-cancelled}.schema.json
proto/buf.yaml  proto/buf.gen.yaml
proto/identity/v1/identity.proto  proto/catalog/v1/catalog.proto
gen/go.mod  gen/identity/v1/*.pb.go  gen/catalog/v1/*.pb.go    (generated, committed)
pkg/go.mod
pkg/logx/        logger.go                 JSON slog + request_id/trace_id enrichment
pkg/httpx/       errors.go json.go middleware.go
pkg/authctx/     authctx.go
pkg/config/      config.go
pkg/event/       event.go
pkg/otelx/       otelx.go
pkg/server/      health.go run.go
pkg/grpcx/       grpcx.go
pkg/testutil/    containers.go openapi.go
pkg/pgxx/        pgxx.go
pkg/kafkax/      producer.go consumer.go
pkg/outbox/      outbox.go relay.go schema.sql
services/{gateway,identity,catalog,order}/
    go.mod  Dockerfile  cmd/server/{main.go,handler.go,handler_test.go}
    internal/{domain,app,adapter}/.gitkeep
    migrations/{migrations.go,000001_init.up.sql,000001_init.down.sql}   (not gateway)
    sqlc.yaml  internal/adapter/postgres/queries/.gitkeep                 (not gateway)
deploy/docker-compose.yml  deploy/docker-compose.web.yml  deploy/postgres/init.sql
```

Every task's tests use `github.com/stretchr/testify` (`require`/`assert`).

---

### Task 1: Workspace scaffolding

**Files:**
- Create: `go.work`, `pkg/go.mod`, `pkg/doc.go`, `gen/go.mod`, `.editorconfig`, `.golangci.yml`, `.dockerignore`, `Makefile`
- Modify: `.gitignore` (remove the `go.work.sum` line)

**Interfaces:**
- Produces: `make tools` (installs pinned tools into `./bin`), `make test`, `make lint-go`, variable `GO_MODULES` (overridable).

- [x] **Step 1: Create `pkg/go.mod`, `pkg/doc.go`, `gen/go.mod`**

```
// pkg/go.mod
module github.com/ngoxuanchien/e-commerce/pkg

go 1.27.1
```

```go
// pkg/doc.go
// Package pkg is the root of the shared libraries used by every marketplace service.
package pkg
```

```
// gen/go.mod
module github.com/ngoxuanchien/e-commerce/gen

go 1.27.1
```

- [x] **Step 2: Create `go.work`** (services are added in Task 16)

```
go 1.27.1

use (
	./gen
	./pkg
)
```

- [x] **Step 3: Create `.editorconfig`**

```ini
root = true

[*]
charset = utf-8
end_of_line = lf
insert_final_newline = true
trim_trailing_whitespace = true
indent_style = space
indent_size = 2

[*.go]
indent_style = tab

[Makefile]
indent_style = tab

[*.md]
trim_trailing_whitespace = false
```

- [x] **Step 4: Create `.golangci.yml`**

```yaml
version: "2"
linters:
  default: standard
  enable:
    - bodyclose
    - errorlint
    - misspell
    - unconvert
formatters:
  enable:
    - gofmt
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/ngoxuanchien/e-commerce
```

- [x] **Step 5: Create `.dockerignore`**

```
.git
bin
web/node_modules
web/dist
deploy/secrets
**/*.test
```

- [x] **Step 6: Create `Makefile`**

```make
SHELL := /bin/bash
.DEFAULT_GOAL := help

GO_MODULES ?= pkg gen services/gateway services/identity services/catalog services/order
BIN := $(CURDIR)/bin
export PATH := $(BIN):$(PATH)

BUF_VERSION := v1.73.0
SQLC_VERSION := v1.31.1
GOLANGCI_LINT_VERSION := v2.14.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
REDOCLY_VERSION := 2.54.3

COMPOSE := docker compose -f deploy/docker-compose.yml $(if $(wildcard web/package.json),-f deploy/docker-compose.web.yml)

# go_tool installs $(2)@$(3) as bin/$(1)-$(3) and points bin/$(1) at it,
# so bumping a version in this file forces a reinstall.
define go_tool
$(BIN)/$(1)-$(3):
	@mkdir -p $(BIN)/.tmp-$(1)
	GOBIN=$(BIN)/.tmp-$(1) go install $(2)@$(3)
	@mv $(BIN)/.tmp-$(1)/$(1) $$@ && rm -rf $(BIN)/.tmp-$(1)
	@ln -sf $(1)-$(3) $(BIN)/$(1)
TOOLS += $(BIN)/$(1)-$(3)
endef

$(eval $(call go_tool,buf,github.com/bufbuild/buf/cmd/buf,$(BUF_VERSION)))
$(eval $(call go_tool,sqlc,github.com/sqlc-dev/sqlc/cmd/sqlc,$(SQLC_VERSION)))
$(eval $(call go_tool,golangci-lint,github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION)))
$(eval $(call go_tool,protoc-gen-go,google.golang.org/protobuf/cmd/protoc-gen-go,$(PROTOC_GEN_GO_VERSION)))
$(eval $(call go_tool,protoc-gen-go-grpc,google.golang.org/grpc/cmd/protoc-gen-go-grpc,$(PROTOC_GEN_GO_GRPC_VERSION)))

.PHONY: help tools gen gen-proto gen-sqlc lint lint-go lint-proto lint-openapi test test-short tidy up up-app down keys seed e2e

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-14s %s\n", $$1, $$2}'

tools: $(TOOLS) ## Install pinned dev tools into ./bin

gen: gen-proto gen-sqlc ## Generate code (buf + sqlc)

gen-proto: tools
	buf generate proto --template proto/buf.gen.yaml
	cd gen && go mod tidy

gen-sqlc: tools
	@for f in services/*/sqlc.yaml; do \
		d=$$(dirname $$f); \
		if ls $$d/internal/adapter/postgres/queries/*.sql >/dev/null 2>&1; then \
			echo "sqlc generate $$d"; (cd $$d && sqlc generate) || exit 1; \
		else echo "sqlc: skip $$d (no queries yet)"; fi; \
	done

lint: lint-go lint-proto lint-openapi ## Run all linters

lint-go: tools
	@for m in $(GO_MODULES); do echo "golangci-lint $$m"; (cd $$m && golangci-lint run ./...) || exit 1; done

lint-proto: tools
	buf lint proto
	buf format --diff --exit-code proto

lint-openapi:
	npx --yes @redocly/cli@$(REDOCLY_VERSION) lint --config api/redocly.yaml api/openapi/*.yaml

test: ## Run all Go tests (needs Docker for container tests)
	@for m in $(GO_MODULES); do echo "go test $$m"; (cd $$m && go test -race -count=1 ./...) || exit 1; done

test-short: ## Run Go tests without containers
	@for m in $(GO_MODULES); do (cd $$m && go test -short -count=1 ./...) || exit 1; done

tidy: ## go mod tidy every module
	@for m in $(GO_MODULES); do (cd $$m && go mod tidy) || exit 1; done

up: ## Start infrastructure only
	$(COMPOSE) up -d --wait

up-app: keys ## Start infrastructure + all services
	$(COMPOSE) --profile app up -d --build --wait

down: ## Stop everything
	$(COMPOSE) --profile app down --remove-orphans

keys: ## Generate the dev JWT signing key (deploy/secrets, gitignored)
	@mkdir -p deploy/secrets
	@test -f deploy/secrets/jwt_private.pem || { \
		openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out deploy/secrets/jwt_private.pem && \
		chmod 644 deploy/secrets/jwt_private.pem && echo "generated deploy/secrets/jwt_private.pem"; }

seed: ## Seed demo data (implemented by WS6)
	@echo "seed: not implemented yet (owned by WS6)"

e2e: ## Run end-to-end tests (implemented by WS6)
	@echo "e2e: not implemented yet (owned by WS6)"
```

Until Task 16 creates the services, run `make test GO_MODULES=pkg` / `make lint-go GO_MODULES=pkg`.

- [x] **Step 7: Edit `.gitignore`** — delete the `go.work.sum` line (the workspace sum file is committed for reproducible builds).

- [x] **Step 8: Verify tools install and the empty module builds**

Run: `make tools && ls bin && (cd pkg && go build ./...)`
Expected: `bin/` contains `buf`, `sqlc`, `golangci-lint`, `protoc-gen-go`, `protoc-gen-go-grpc` symlinks; build exits 0.

- [x] **Step 9: Commit**

```bash
git add go.work pkg gen .editorconfig .golangci.yml .dockerignore Makefile .gitignore
git commit -m "chore: add Go workspace, pinned tooling and Makefile"
```

---

### Task 2: `pkg/logx` + `pkg/httpx` errors and JSON

**Files:**
- Create: `pkg/logx/logger.go`, `pkg/logx/logger_test.go`, `pkg/httpx/errors.go`, `pkg/httpx/json.go`, `pkg/httpx/errors_test.go`, `pkg/httpx/json_test.go`

**Interfaces:**
- Produces (`logx`): `New(w io.Writer, level string) *slog.Logger`, `ParseLevel(string) slog.Level`, `WithRequestID(ctx, id string) context.Context`, `RequestID(ctx) string`.
- Produces (`httpx`): code constants `CodeValidation … CodeUpstreamUnavailable`; `StatusFor(code string) int`; `type AppError struct{Code string; Status int; Message string; Details map[string]any; Err error}` with `Error()`, `Unwrap()`, `WithDetails(map[string]any) *AppError`, `Wrap(error) *AppError`; `NewError(code, message string) *AppError`; `ValidationError(fields map[string]string) *AppError`; `WriteJSON(w, status int, v any)`; `WriteError(w, r, err error)`; `Decode(r *http.Request, dst any) error`; `Validate(v any) error`; `const MaxBodyBytes = 1 << 20`.

- [x] **Step 1: Add dependencies**

Run: `cd pkg && go get github.com/stretchr/testify@v1.12.1 go.opentelemetry.io/otel@v1.46.0 github.com/go-playground/validator/v10@v10.30.5 github.com/google/uuid@v1.6.0`

- [x] **Step 2: Write the failing logx tests** — `pkg/logx/logger_test.go`

```go
package logx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/ngoxuanchien/e-commerce/pkg/logx"
)

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	return got
}

func TestLoggerAddsRequestAndTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	log := logx.New(&buf, "debug")

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled,
	}))
	ctx = logx.WithRequestID(ctx, "req-1")

	log.InfoContext(ctx, "hello", "k", "v")

	got := decode(t, &buf)
	assert.Equal(t, "hello", got["msg"])
	assert.Equal(t, "v", got["k"])
	assert.Equal(t, "req-1", got["request_id"])
	assert.Equal(t, "0102030405060708090a0b0c0d0e0f10", got["trace_id"])
	assert.Equal(t, "0102030405060708", got["span_id"])
}

func TestLoggerOmitsMissingContextValues(t *testing.T) {
	var buf bytes.Buffer
	logx.New(&buf, "info").With("svc", "x").InfoContext(context.Background(), "hi")

	got := decode(t, &buf)
	assert.NotContains(t, got, "request_id")
	assert.NotContains(t, got, "trace_id")
	assert.Equal(t, "x", got["svc"])
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logx.New(&buf, "warn").Info("dropped")
	assert.Empty(t, buf.String())
}

func TestParseLevel(t *testing.T) {
	assert.Equal(t, slog.LevelDebug, logx.ParseLevel("debug"))
	assert.Equal(t, slog.LevelWarn, logx.ParseLevel("WARN"))
	assert.Equal(t, slog.LevelInfo, logx.ParseLevel("bogus"))
	assert.Equal(t, slog.LevelInfo, logx.ParseLevel(""))
}

func TestRequestIDRoundTrip(t *testing.T) {
	assert.Equal(t, "", logx.RequestID(context.Background()))
	assert.Equal(t, "abc", logx.RequestID(logx.WithRequestID(context.Background(), "abc")))
}
```

- [x] **Step 3: Run to verify failure** — `cd pkg && go test ./logx/` → FAIL, `undefined: logx.New`.

- [x] **Step 4: Implement `pkg/logx/logger.go`**

```go
// Package logx builds the JSON slog logger used by every service. Records
// logged with a context automatically carry request_id, trace_id and span_id.
package logx

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

type requestIDKey struct{}

// WithRequestID stores the request ID in ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request ID stored in ctx, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// New returns a JSON logger writing to w at the given level ("debug", "info", "warn", "error").
func New(w io.Writer, level string) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: ParseLevel(level)})
	return slog.New(contextHandler{h})
}

// ParseLevel parses a level name case-insensitively, defaulting to info.
func ParseLevel(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return l
}

type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
```

- [x] **Step 5: Run logx tests** — `cd pkg && go test ./logx/` → PASS.

- [x] **Step 6: Write failing httpx error tests** — `pkg/httpx/errors_test.go`

```go
package httpx_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

func TestStatusFor(t *testing.T) {
	cases := map[string]int{
		httpx.CodeValidation:             400,
		httpx.CodeUnauthenticated:        401,
		httpx.CodeForbidden:              403,
		httpx.CodeNotFound:               404,
		httpx.CodeConflict:               409,
		httpx.CodeOutOfStock:             409,
		httpx.CodeInvalidStateTransition: 409,
		httpx.CodeIdempotencyKeyReused:   422,
		httpx.CodeRateLimited:            429,
		httpx.CodeInternal:               500,
		httpx.CodeUpstreamUnavailable:    503,
		"SOMETHING_ELSE":                 500,
	}
	for code, want := range cases {
		assert.Equal(t, want, httpx.StatusFor(code), code)
	}
}

type body struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func serveError(t *testing.T, err error) (*httptest.ResponseRecorder, body) {
	t.Helper()
	rec := httptest.NewRecorder()
	httpx.WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), err)
	var b body
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &b))
	return rec, b
}

func TestWriteErrorAppError(t *testing.T) {
	err := httpx.NewError(httpx.CodeOutOfStock, "not enough stock").
		WithDetails(map[string]any{"shortages": []any{map[string]any{"sku_id": "s1"}}})

	rec, b := serveError(t, err)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "OUT_OF_STOCK", b.Error.Code)
	assert.Equal(t, "not enough stock", b.Error.Message)
	assert.Contains(t, b.Error.Details, "shortages")
}

func TestWriteErrorWrappedAppError(t *testing.T) {
	err := fmt.Errorf("load order: %w", httpx.NewError(httpx.CodeNotFound, "order not found"))
	rec, b := serveError(t, err)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "NOT_FOUND", b.Error.Code)
}

func TestWriteErrorUnknownErrorIsInternalAndDoesNotLeak(t *testing.T) {
	rec, b := serveError(t, errors.New("pq: password=secret"))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "INTERNAL", b.Error.Code)
	assert.Equal(t, "internal server error", b.Error.Message)
	assert.NotContains(t, rec.Body.String(), "secret")
}

func TestWriteErrorOmitsEmptyDetails(t *testing.T) {
	rec, _ := serveError(t, httpx.NewError(httpx.CodeForbidden, "no"))
	assert.NotContains(t, rec.Body.String(), "details")
}

func TestAppErrorWrapKeepsCause(t *testing.T) {
	cause := errors.New("dial tcp: refused")
	err := httpx.NewError(httpx.CodeUpstreamUnavailable, "catalog unavailable").Wrap(cause)
	assert.ErrorIs(t, err, cause)
	assert.Equal(t, 503, err.Status)
}

func TestValidationErrorDetails(t *testing.T) {
	err := httpx.ValidationError(map[string]string{"email": "is required"})
	assert.Equal(t, 400, err.Status)
	assert.Equal(t, map[string]any{"fields": map[string]string{"email": "is required"}}, err.Details)
}
```

- [x] **Step 7: Write failing Decode tests** — `pkg/httpx/json_test.go`

```go
package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

type skuIn struct {
	Name  string `json:"name" validate:"required"`
	Price int64  `json:"price" validate:"min=1000"`
}

type productIn struct {
	FullName string  `json:"full_name" validate:"required,max=10"`
	Skus     []skuIn `json:"skus" validate:"required,min=1,dive"`
}

func req(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
}

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	var ae *httpx.AppError
	require.True(t, errors.As(err, &ae), "want *AppError, got %v", err)
	require.Equal(t, httpx.CodeValidation, ae.Code)
	fields, _ := ae.Details["fields"].(map[string]string)
	return fields
}

func TestDecodeValid(t *testing.T) {
	var in productIn
	require.NoError(t, httpx.Decode(req(`{"full_name":"An","skus":[{"name":"M","price":1000}]}`), &in))
	assert.Equal(t, "An", in.FullName)
	assert.Len(t, in.Skus, 1)
}

func TestDecodeReportsJSONFieldPaths(t *testing.T) {
	var in productIn
	err := httpx.Decode(req(`{"full_name":"","skus":[{"name":"","price":5}]}`), &in)
	fields := fieldsOf(t, err)
	assert.Equal(t, "is required", fields["full_name"])
	assert.Equal(t, "is required", fields["skus[0].name"])
	assert.Equal(t, "must be at least 1000", fields["skus[0].price"])
}

func TestDecodeRejectsMalformedBodies(t *testing.T) {
	cases := map[string]string{
		"unknown field": `{"full_name":"a","skus":[{"name":"x","price":1000}],"extra":1}`,
		"trailing data": `{"full_name":"a","skus":[{"name":"x","price":1000}]} {}`,
		"empty":         ``,
		"not json":      `full_name=a`,
		"wrong type":    `{"full_name":5}`,
		"too large":     `{"full_name":"` + strings.Repeat("a", httpx.MaxBodyBytes) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var in productIn
			err := httpx.Decode(req(body), &in)
			require.Error(t, err)
			var ae *httpx.AppError
			require.True(t, errors.As(err, &ae))
			assert.Equal(t, http.StatusBadRequest, ae.Status)
			assert.Contains(t, ae.Details["fields"], "body")
		})
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteJSON(rec, http.StatusCreated, map[string]string{"id": "1"})
	assert.Equal(t, 201, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"id":"1"}`, rec.Body.String())
}

func TestWriteJSONNilBody(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteJSON(rec, http.StatusNoContent, nil)
	assert.Equal(t, 204, rec.Code)
	assert.Empty(t, rec.Body.String())
}
```

- [x] **Step 8: Run to verify failure** — `cd pkg && go test ./httpx/` → FAIL, `undefined: httpx.CodeValidation`.

- [x] **Step 9: Implement `pkg/httpx/errors.go`**

```go
// Package httpx holds the HTTP conventions shared by all services: the error
// model, JSON encoding/decoding with validation, and common middleware.
package httpx

import (
	"errors"
	"log/slog"
	"net/http"
)

// Error codes (WS0 §3).
const (
	CodeValidation             = "VALIDATION_ERROR"
	CodeUnauthenticated        = "UNAUTHENTICATED"
	CodeForbidden              = "FORBIDDEN"
	CodeNotFound               = "NOT_FOUND"
	CodeConflict               = "CONFLICT"
	CodeOutOfStock             = "OUT_OF_STOCK"
	CodeInvalidStateTransition = "INVALID_STATE_TRANSITION"
	CodeIdempotencyKeyReused   = "IDEMPOTENCY_KEY_REUSED"
	CodeRateLimited            = "RATE_LIMITED"
	CodeInternal               = "INTERNAL"
	CodeUpstreamUnavailable    = "UPSTREAM_UNAVAILABLE"
)

var codeStatus = map[string]int{
	CodeValidation:             http.StatusBadRequest,
	CodeUnauthenticated:        http.StatusUnauthorized,
	CodeForbidden:              http.StatusForbidden,
	CodeNotFound:               http.StatusNotFound,
	CodeConflict:               http.StatusConflict,
	CodeOutOfStock:             http.StatusConflict,
	CodeInvalidStateTransition: http.StatusConflict,
	CodeIdempotencyKeyReused:   http.StatusUnprocessableEntity,
	CodeRateLimited:            http.StatusTooManyRequests,
	CodeInternal:               http.StatusInternalServerError,
	CodeUpstreamUnavailable:    http.StatusServiceUnavailable,
}

// StatusFor maps an error code to its HTTP status; unknown codes map to 500.
func StatusFor(code string) int {
	if s, ok := codeStatus[code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// AppError is an error that is safe to return to API clients.
// Err is the internal cause; it is logged but never serialized.
type AppError struct {
	Code    string
	Status  int
	Message string
	Details map[string]any
	Err     error
}

// NewError builds an AppError whose status is derived from code.
func NewError(code, message string) *AppError {
	return &AppError{Code: code, Status: StatusFor(code), Message: message}
}

// ValidationError builds a VALIDATION_ERROR whose details are {"fields": {field: message}}.
func ValidationError(fields map[string]string) *AppError {
	return NewError(CodeValidation, "request validation failed").WithDetails(map[string]any{"fields": fields})
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Message + ": " + e.Err.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *AppError) Unwrap() error { return e.Err }

// WithDetails returns a copy of e with details set.
func (e *AppError) WithDetails(details map[string]any) *AppError {
	c := *e
	c.Details = details
	return &c
}

// Wrap returns a copy of e with the internal cause set.
func (e *AppError) Wrap(err error) *AppError {
	c := *e
	c.Err = err
	return &c
}

type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// WriteError writes err in the standard error format. Errors that are not
// *AppError become a generic 500 INTERNAL; their text is logged, never returned.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *AppError
	if !errors.As(err, &ae) {
		slog.ErrorContext(r.Context(), "unhandled error", "error", err)
		ae = NewError(CodeInternal, "internal server error")
	} else if ae.Status >= http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "server error", "code", ae.Code, "error", err)
	}
	WriteJSON(w, ae.Status, errorBody{Error: errorPayload{Code: ae.Code, Message: ae.Message, Details: ae.Details}})
}
```

- [x] **Step 10: Implement `pkg/httpx/json.go`**

```go
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// MaxBodyBytes is the largest request body Decode accepts.
const MaxBodyBytes = 1 << 20

var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	// Report field paths using JSON names so clients can map them to form fields.
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch name {
		case "-":
			return ""
		case "":
			return f.Name
		}
		return name
	})
	return v
}

// WriteJSON writes v as JSON with the given status. A nil v writes no body.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	if v == nil {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Decode reads a single JSON object from the request body into dst (unknown
// fields rejected, max MaxBodyBytes) and validates it with `validate` tags.
// Every failure is a 400 VALIDATION_ERROR *AppError.
func Decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return bodyError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ValidationError(map[string]string{"body": "must contain a single JSON object"})
	}
	return Validate(dst)
}

func bodyError(err error) *AppError {
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe):
		return ValidationError(map[string]string{"body": fmt.Sprintf("must not exceed %d bytes", MaxBodyBytes)})
	case errors.Is(err, io.EOF):
		return ValidationError(map[string]string{"body": "is required"})
	default:
		return ValidationError(map[string]string{"body": "invalid JSON: " + err.Error()})
	}
}

// Validate runs struct validation and converts failures to a VALIDATION_ERROR.
func Validate(v any) error {
	err := validate.Struct(v)
	if err == nil {
		return nil
	}
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return NewError(CodeInternal, "internal server error").Wrap(err)
	}
	fields := make(map[string]string, len(ve))
	for _, fe := range ve {
		fields[fieldPath(fe)] = fieldMessage(fe)
	}
	return ValidationError(fields)
}

// fieldPath drops the top-level struct name: "productIn.skus[0].name" → "skus[0].name".
func fieldPath(fe validator.FieldError) string {
	ns := fe.Namespace()
	if _, rest, ok := strings.Cut(ns, "."); ok {
		return rest
	}
	return ns
}

func fieldMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email"
	case "uuid", "uuid4", "uuid7":
		return "must be a valid UUID"
	case "min", "gte":
		return "must be at least " + fe.Param()
	case "max", "lte":
		return "must be at most " + fe.Param()
	case "len":
		return "must have length " + fe.Param()
	case "oneof":
		return "must be one of: " + fe.Param()
	default:
		return "failed '" + fe.Tag() + "' validation"
	}
}
```

- [x] **Step 11: Run tests** — `cd pkg && go test ./logx/ ./httpx/` → PASS.

- [x] **Step 12: Commit**

```bash
git add pkg
git commit -m "feat(pkg): add logx logger and httpx error model with JSON decoding"
```

---

### Task 3: `pkg/httpx` middleware

**Files:**
- Create: `pkg/httpx/middleware.go`, `pkg/httpx/middleware_test.go`

**Interfaces:**
- Consumes: `logx.WithRequestID`, `logx.RequestID`, `WriteError`, `NewError`.
- Produces: `const HeaderRequestID = "X-Request-Id"`; `RequestID(next http.Handler) http.Handler`; `Recover(next http.Handler) http.Handler`; `AccessLog(logger *slog.Logger) func(http.Handler) http.Handler`; `NotFound() http.Handler`.

- [x] **Step 1: Write failing tests** — `pkg/httpx/middleware_test.go`

```go
package httpx_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
)

func captureRequestID(seen *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = logx.RequestID(r.Context())
	})
}

func TestRequestIDGeneratesWhenMissing(t *testing.T) {
	var seen string
	rec := httptest.NewRecorder()
	httpx.RequestID(captureRequestID(&seen)).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	id := rec.Header().Get(httpx.HeaderRequestID)
	parsed, err := uuid.Parse(id)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), parsed.Version())
	assert.Equal(t, id, seen)
}

func TestRequestIDReusesValidIncoming(t *testing.T) {
	var seen string
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(httpx.HeaderRequestID, "abc-123_x.y")
	rec := httptest.NewRecorder()
	httpx.RequestID(captureRequestID(&seen)).ServeHTTP(rec, r)

	assert.Equal(t, "abc-123_x.y", rec.Header().Get(httpx.HeaderRequestID))
	assert.Equal(t, "abc-123_x.y", seen)
}

func TestRequestIDReplacesInvalidIncoming(t *testing.T) {
	for _, bad := range []string{"has space", "line\nbreak", strings.Repeat("a", 129), `"quoted"`} {
		var seen string
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set(httpx.HeaderRequestID, bad)
		rec := httptest.NewRecorder()
		httpx.RequestID(captureRequestID(&seen)).ServeHTTP(rec, r)

		assert.NotEqual(t, bad, seen)
		_, err := uuid.Parse(seen)
		assert.NoError(t, err, "input %q", bad)
	}
}

func TestRequestIDIsForwardedOnRequestHeader(t *testing.T) {
	var header string
	h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get(httpx.HeaderRequestID)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	assert.Equal(t, rec.Header().Get(httpx.HeaderRequestID), header)
}

func TestRecoverTurnsPanicIntoInternalError(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"INTERNAL","message":"internal server error"}}`, rec.Body.String())
}

func TestAccessLogRecordsRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := logx.New(&buf, "info")
	h := httpx.RequestID(httpx.AccessLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/things?secret=1", nil))

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "http request", entry["msg"])
	assert.Equal(t, "POST", entry["method"])
	assert.Equal(t, "/api/things", entry["path"])
	assert.EqualValues(t, 201, entry["status"])
	assert.EqualValues(t, 5, entry["bytes"])
	assert.NotEmpty(t, entry["request_id"])
	assert.Contains(t, entry, "duration_ms")
}

func TestAccessLogDefaultsStatusTo200(t *testing.T) {
	var buf bytes.Buffer
	h := httpx.AccessLog(logx.New(&buf, "info"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	assert.Contains(t, buf.String(), `"status":200`)
}

func TestNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.NotFound().ServeHTTP(rec, httptest.NewRequest("GET", "/nope", nil))
	assert.Equal(t, 404, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"NOT_FOUND","message":"route not found"}}`, rec.Body.String())
}
```

- [x] **Step 2: Run to verify failure** — `cd pkg && go test ./httpx/` → FAIL, `undefined: httpx.RequestID`.

- [x] **Step 3: Implement `pkg/httpx/middleware.go`**

```go
package httpx

import (
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/logx"
)

// HeaderRequestID carries the request ID between clients, the gateway and services.
const HeaderRequestID = "X-Request-Id"

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// RequestID reuses a well-formed incoming X-Request-Id or generates a UUIDv7.
// The ID is stored in the context, set on the request (so proxies forward it)
// and echoed in the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !validRequestID.MatchString(id) {
			id = newRequestID()
		}
		r.Header.Set(HeaderRequestID, id)
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(logx.WithRequestID(r.Context(), id)))
	})
}

func newRequestID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

// Recover converts panics into a 500 INTERNAL response.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
				panic(v)
			}
			slog.ErrorContext(r.Context(), "panic recovered", "panic", v, "stack", string(debug.Stack()))
			WriteError(w, r, NewError(CodeInternal, "internal server error"))
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog logs one line per request: method, path (no query), status, bytes, duration.
// It never logs headers or bodies.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			logger.LogAttrs(r.Context(), slog.LevelInfo, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			)
		})
	}
}

// NotFound responds 404 NOT_FOUND in the standard error format.
func NotFound() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, NewError(CodeNotFound, "route not found"))
	})
}
```

- [x] **Step 4: Run tests** — `cd pkg && go test ./httpx/` → PASS.

- [x] **Step 5: Commit**

```bash
git add pkg/httpx
git commit -m "feat(pkg/httpx): add request ID, recover and access log middleware"
```

---

### Task 4: `pkg/authctx`

**Files:**
- Create: `pkg/authctx/authctx.go`, `pkg/authctx/authctx_test.go`

**Interfaces:**
- Consumes: `httpx.WriteError`, `httpx.NewError`, `httpx.CodeUnauthenticated`, `httpx.CodeForbidden`.
- Produces: `HeaderUserID`, `HeaderUserRoles`, `RoleBuyer`, `RoleSeller`, `RoleAdmin`; `type Identity struct{UserID string; Roles []string}`; `WithIdentity(ctx, Identity) context.Context`; `FromContext(ctx) (Identity, bool)`; `UserID(ctx) string`; `HasRole(ctx, role string) bool`; `ParseRoles(string) []string`; `Middleware(http.Handler) http.Handler`; `RequireUser(http.Handler) http.Handler`; `RequireRole(role string) func(http.Handler) http.Handler`.

- [x] **Step 1: Write failing tests** — `pkg/authctx/authctx_test.go`

```go
package authctx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
)

func TestParseRoles(t *testing.T) {
	assert.Equal(t, []string{"buyer", "seller"}, authctx.ParseRoles(" buyer, seller,,"))
	assert.Equal(t, []string{"buyer"}, authctx.ParseRoles("buyer,buyer"))
	assert.Empty(t, authctx.ParseRoles(""))
	assert.Empty(t, authctx.ParseRoles(" , "))
}

func serve(h http.Handler, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/", nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestMiddlewarePopulatesIdentity(t *testing.T) {
	var got authctx.Identity
	var ok bool
	h := authctx.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = authctx.FromContext(r.Context())
	}))
	serve(h, map[string]string{authctx.HeaderUserID: "u1", authctx.HeaderUserRoles: "buyer, seller"})

	assert.True(t, ok)
	assert.Equal(t, authctx.Identity{UserID: "u1", Roles: []string{"buyer", "seller"}}, got)
}

func TestMiddlewareIgnoresRolesWithoutUser(t *testing.T) {
	var ok bool
	h := authctx.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, ok = authctx.FromContext(r.Context())
	}))
	serve(h, map[string]string{authctx.HeaderUserID: "  ", authctx.HeaderUserRoles: "seller"})
	assert.False(t, ok)
}

func TestHelpersWithoutIdentity(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "", authctx.UserID(ctx))
	assert.False(t, authctx.HasRole(ctx, authctx.RoleSeller))
}

func TestHasRole(t *testing.T) {
	ctx := authctx.WithIdentity(context.Background(), authctx.Identity{UserID: "u1", Roles: []string{"buyer"}})
	assert.Equal(t, "u1", authctx.UserID(ctx))
	assert.True(t, authctx.HasRole(ctx, authctx.RoleBuyer))
	assert.False(t, authctx.HasRole(ctx, authctx.RoleSeller))
}

var ok200 = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

func TestRequireUser(t *testing.T) {
	h := authctx.Middleware(authctx.RequireUser(ok200))

	rec := serve(h, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"UNAUTHENTICATED","message":"authentication required"}}`, rec.Body.String())

	assert.Equal(t, http.StatusOK, serve(h, map[string]string{authctx.HeaderUserID: "u1"}).Code)
}

func TestRequireRole(t *testing.T) {
	h := authctx.Middleware(authctx.RequireRole(authctx.RoleSeller)(ok200))

	assert.Equal(t, http.StatusUnauthorized, serve(h, nil).Code)

	rec := serve(h, map[string]string{authctx.HeaderUserID: "u1", authctx.HeaderUserRoles: "buyer"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"FORBIDDEN","message":"requires role seller"}}`, rec.Body.String())

	assert.Equal(t, http.StatusOK,
		serve(h, map[string]string{authctx.HeaderUserID: "u1", authctx.HeaderUserRoles: "buyer,seller"}).Code)
}
```

- [x] **Step 2: Run to verify failure** — `cd pkg && go test ./authctx/` → FAIL, `undefined: authctx.ParseRoles`.

- [x] **Step 3: Implement `pkg/authctx/authctx.go`**

```go
// Package authctx carries the caller identity injected by the gateway
// (X-User-Id / X-User-Roles) through request contexts. Services trust these
// headers because only the gateway is reachable from outside.
package authctx

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

const (
	HeaderUserID    = "X-User-Id"
	HeaderUserRoles = "X-User-Roles"

	RoleBuyer  = "buyer"
	RoleSeller = "seller"
	RoleAdmin  = "admin"
)

// Identity is the authenticated caller.
type Identity struct {
	UserID string
	Roles  []string
}

type identityKey struct{}

// WithIdentity stores id in ctx.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// FromContext returns the caller identity, if any.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// UserID returns the caller's user ID, or "" for anonymous requests.
func UserID(ctx context.Context) string {
	id, _ := FromContext(ctx)
	return id.UserID
}

// HasRole reports whether the caller has role.
func HasRole(ctx context.Context, role string) bool {
	id, _ := FromContext(ctx)
	return slices.Contains(id.Roles, role)
}

// ParseRoles splits a comma-separated role list, trimming blanks and duplicates.
func ParseRoles(s string) []string {
	var roles []string
	for _, r := range strings.Split(s, ",") {
		r = strings.TrimSpace(r)
		if r != "" && !slices.Contains(roles, r) {
			roles = append(roles, r)
		}
	}
	return roles
}

// Middleware reads the identity headers into the request context.
// A blank X-User-Id means an anonymous request, regardless of X-User-Roles.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if uid := strings.TrimSpace(r.Header.Get(HeaderUserID)); uid != "" {
			id := Identity{UserID: uid, Roles: ParseRoles(r.Header.Get(HeaderUserRoles))}
			r = r.WithContext(WithIdentity(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}

// RequireUser rejects anonymous requests with 401 UNAUTHENTICATED.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserID(r.Context()) == "" {
			httpx.WriteError(w, r, httpx.NewError(httpx.CodeUnauthenticated, "authentication required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole rejects anonymous requests with 401 and callers lacking role with 403.
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !HasRole(r.Context(), role) {
				httpx.WriteError(w, r, httpx.NewError(httpx.CodeForbidden, "requires role "+role))
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}
```

- [x] **Step 4: Run tests** — `cd pkg && go test ./authctx/` → PASS.

- [x] **Step 5: Commit**

```bash
git add pkg/authctx
git commit -m "feat(pkg/authctx): add identity context and auth middleware"
```

---

### Task 5: `pkg/config`

**Files:**
- Create: `pkg/config/config.go`, `pkg/config/config_test.go`

**Interfaces:**
- Produces: `type Base struct{ServiceName, HTTPAddr, GRPCAddr, DatabaseURL, RedisURL string; KafkaBrokers []string; OTLPEndpoint, LogLevel string; ShutdownTimeout time.Duration}`; `Load[T any]() (T, error)`.

- [x] **Step 1: Add dependency** — `cd pkg && go get github.com/caarlos0/env/v11@v11.4.1`

- [x] **Step 2: Write failing tests** — `pkg/config/config_test.go`

```go
package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

type svcConfig struct {
	config.Base
	PendingTimeout time.Duration `env:"PENDING_TIMEOUT" envDefault:"2m"`
}

func TestLoadParsesBaseAndServiceFields(t *testing.T) {
	t.Setenv("SERVICE_NAME", "order")
	t.Setenv("HTTP_ADDR", ":8083")
	t.Setenv("KAFKA_BROKERS", "k1:9092,k2:9092")
	t.Setenv("PENDING_TIMEOUT", "30s")

	cfg, err := config.Load[svcConfig]()
	require.NoError(t, err)

	assert.Equal(t, "order", cfg.ServiceName)
	assert.Equal(t, ":8083", cfg.HTTPAddr)
	assert.Equal(t, []string{"k1:9092", "k2:9092"}, cfg.KafkaBrokers)
	assert.Equal(t, 30*time.Second, cfg.PendingTimeout)
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("SERVICE_NAME", "gateway")

	cfg, err := config.Load[svcConfig]()
	require.NoError(t, err)

	assert.Equal(t, ":8080", cfg.HTTPAddr)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, 15*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, 2*time.Minute, cfg.PendingTimeout)
	assert.Empty(t, cfg.KafkaBrokers)
}

func TestLoadRequiresServiceName(t *testing.T) {
	t.Setenv("SERVICE_NAME", "")
	_, err := config.Load[svcConfig]()
	assert.ErrorContains(t, err, "SERVICE_NAME")
}

func TestLoadRejectsBadDuration(t *testing.T) {
	t.Setenv("SERVICE_NAME", "x")
	t.Setenv("PENDING_TIMEOUT", "soon")
	_, err := config.Load[svcConfig]()
	assert.ErrorContains(t, err, "PENDING_TIMEOUT")
}
```

- [x] **Step 3: Run to verify failure** — `cd pkg && go test ./config/` → FAIL, `undefined: config.Base`.

- [x] **Step 4: Implement `pkg/config/config.go`**

```go
// Package config loads service configuration from environment variables.
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Base holds the variables every service understands (WS0 §3). Services embed
// it in their own config struct and add service-specific fields.
type Base struct {
	ServiceName     string        `env:"SERVICE_NAME,required,notEmpty"`
	HTTPAddr        string        `env:"HTTP_ADDR" envDefault:":8080"`
	GRPCAddr        string        `env:"GRPC_ADDR"`
	DatabaseURL     string        `env:"DATABASE_URL"`
	RedisURL        string        `env:"REDIS_URL"`
	KafkaBrokers    []string      `env:"KAFKA_BROKERS" envSeparator:","`
	OTLPEndpoint    string        `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	LogLevel        string        `env:"LOG_LEVEL" envDefault:"info"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
}

// Load parses the environment into a new T.
func Load[T any]() (T, error) {
	var cfg T
	if err := env.Parse(&cfg); err != nil {
		return cfg, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}
```

- [x] **Step 5: Run tests** — `cd pkg && go test ./config/` → PASS.

- [x] **Step 6: Commit**

```bash
git add pkg
git commit -m "feat(pkg/config): add env-based config loader"
```

---

### Task 6: Event contract — `api/events` schemas + `pkg/event`

**Files:**
- Create: `api/events/envelope.schema.json`, `api/events/order-created.schema.json`, `api/events/order-status-changed.schema.json`, `api/events/order-cancelled.schema.json`, `pkg/event/event.go`, `pkg/event/event_test.go`

**Interfaces:**
- Produces: `const TopicOrderEvents = "order.events"`, `const Version = 1`; `type Type string` with `TypeOrderCreated`, `TypeOrderStatusChanged`, `TypeOrderCancelled`; `type Payload interface{ EventType() Type }`; `type Envelope struct{EventID string; EventType Type; Version int; OccurredAt time.Time; Data json.RawMessage}`; `type OrderItem struct{SkuID string; Quantity int32; UnitPrice int64}`; `type OrderCreated struct{OrderID, CheckoutID, BuyerID, ShopID, SellerUserID string; Total int64; Items []OrderItem}`; `type OrderStatusChanged struct{OrderID, From, To, ActorID, ActorRole string}`; `type OrderCancelled struct{OrderID, Reason, CancelledBy string}`; `CancelledByBuyer/Seller/System`; `New(p Payload, occurredAt time.Time) (Envelope, error)`; `Encode(p Payload, occurredAt time.Time) ([]byte, error)`; `Decode(b []byte) (Envelope, error)`; `DecodeData[T Payload](e Envelope) (T, error)`; errors `ErrUnsupportedVersion`, `ErrInvalidEnvelope`, `ErrTypeMismatch`.

- [x] **Step 1: Write the JSON Schemas**

`api/events/envelope.schema.json`:
```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "EventEnvelope",
  "description": "Envelope of every message on Kafka topic order.events (key = order_id).",
  "type": "object",
  "additionalProperties": false,
  "required": ["event_id", "event_type", "version", "occurred_at", "data"],
  "properties": {
    "event_id": { "type": "string", "format": "uuid", "description": "UUIDv7; consumers deduplicate on it." },
    "event_type": { "enum": ["OrderCreated", "OrderStatusChanged", "OrderCancelled"] },
    "version": { "const": 1 },
    "occurred_at": { "type": "string", "format": "date-time" },
    "data": { "type": "object" }
  }
}
```

`api/events/order-created.schema.json`:
```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "OrderCreated",
  "allOf": [{ "$ref": "envelope.schema.json" }],
  "properties": {
    "event_type": { "const": "OrderCreated" },
    "data": {
      "type": "object",
      "additionalProperties": false,
      "required": ["order_id", "checkout_id", "buyer_id", "shop_id", "seller_user_id", "total", "items"],
      "properties": {
        "order_id": { "type": "string", "format": "uuid" },
        "checkout_id": { "type": "string", "format": "uuid" },
        "buyer_id": { "type": "string", "format": "uuid" },
        "shop_id": { "type": "string", "format": "uuid" },
        "seller_user_id": { "type": "string", "format": "uuid" },
        "total": { "type": "integer", "minimum": 0 },
        "items": {
          "type": "array",
          "minItems": 1,
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["sku_id", "quantity", "unit_price"],
            "properties": {
              "sku_id": { "type": "string", "format": "uuid" },
              "quantity": { "type": "integer", "minimum": 1 },
              "unit_price": { "type": "integer", "minimum": 0 }
            }
          }
        }
      }
    }
  }
}
```

`api/events/order-status-changed.schema.json`:
```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "OrderStatusChanged",
  "allOf": [{ "$ref": "envelope.schema.json" }],
  "$defs": {
    "status": { "enum": ["PENDING", "PENDING_CONFIRMATION", "CONFIRMED", "SHIPPING", "DELIVERED", "COMPLETED", "CANCELLED"] }
  },
  "properties": {
    "event_type": { "const": "OrderStatusChanged" },
    "data": {
      "type": "object",
      "additionalProperties": false,
      "required": ["order_id", "from", "to", "actor_role"],
      "properties": {
        "order_id": { "type": "string", "format": "uuid" },
        "from": { "$ref": "#/$defs/status" },
        "to": { "$ref": "#/$defs/status" },
        "actor_id": { "type": "string", "format": "uuid", "description": "Absent when actor_role is system." },
        "actor_role": { "enum": ["buyer", "seller", "system"] }
      }
    }
  }
}
```

`api/events/order-cancelled.schema.json`:
```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "OrderCancelled",
  "allOf": [{ "$ref": "envelope.schema.json" }],
  "properties": {
    "event_type": { "const": "OrderCancelled" },
    "data": {
      "type": "object",
      "additionalProperties": false,
      "required": ["order_id", "reason", "cancelled_by"],
      "properties": {
        "order_id": { "type": "string", "format": "uuid" },
        "reason": { "type": "string", "minLength": 1 },
        "cancelled_by": { "enum": ["buyer", "seller", "system"] }
      }
    }
  }
}
```

- [x] **Step 2: Add dependency** — `cd pkg && go get github.com/santhosh-tekuri/jsonschema/v6@v6.0.3`

- [x] **Step 3: Write failing tests** — `pkg/event/event_test.go`

```go
package event_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
)

const (
	orderID = "0192a1b2-0000-7000-8000-000000000001"
	userID  = "0192a1b2-0000-7000-8000-000000000002"
	skuID   = "0192a1b2-0000-7000-8000-000000000003"
)

var at = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

func samples() map[string]event.Payload {
	return map[string]event.Payload{
		"order-created.schema.json": event.OrderCreated{
			OrderID: orderID, CheckoutID: orderID, BuyerID: userID, ShopID: orderID, SellerUserID: userID,
			Total: 130000, Items: []event.OrderItem{{SkuID: skuID, Quantity: 2, UnitPrice: 50000}},
		},
		"order-status-changed.schema.json": event.OrderStatusChanged{
			OrderID: orderID, From: "PENDING_CONFIRMATION", To: "CONFIRMED", ActorID: userID, ActorRole: "seller",
		},
		"order-cancelled.schema.json": event.OrderCancelled{
			OrderID: orderID, Reason: "OUT_OF_STOCK", CancelledBy: event.CancelledBySystem,
		},
	}
}

func TestEncodedEventsMatchJSONSchemas(t *testing.T) {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	for file, p := range samples() {
		t.Run(file, func(t *testing.T) {
			sch, err := c.Compile(filepath.Join("..", "..", "api", "events", file))
			require.NoError(t, err)

			b, err := event.Encode(p, at)
			require.NoError(t, err)
			inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
			require.NoError(t, err)
			assert.NoError(t, sch.Validate(inst))
		})
	}
}

func TestSystemStatusChangeOmitsActorID(t *testing.T) {
	b, err := event.Encode(event.OrderStatusChanged{OrderID: orderID, From: "PENDING", To: "CANCELLED", ActorRole: "system"}, at)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "actor_id")
}

func TestNewSetsEnvelopeFields(t *testing.T) {
	env, err := event.New(event.OrderCancelled{OrderID: orderID, Reason: "r", CancelledBy: "buyer"}, at.In(time.FixedZone("ICT", 7*3600)))
	require.NoError(t, err)

	id, err := uuid.Parse(env.EventID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), id.Version())
	assert.Equal(t, event.TypeOrderCancelled, env.EventType)
	assert.Equal(t, 1, env.Version)
	assert.Equal(t, time.UTC, env.OccurredAt.Location())
	assert.True(t, env.OccurredAt.Equal(at))
}

func TestRoundTrip(t *testing.T) {
	for _, p := range samples() {
		b, err := event.Encode(p, at)
		require.NoError(t, err)
		env, err := event.Decode(b)
		require.NoError(t, err)
		assert.Equal(t, p.EventType(), env.EventType)

		switch want := p.(type) {
		case event.OrderCreated:
			got, err := event.DecodeData[event.OrderCreated](env)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		case event.OrderStatusChanged:
			got, err := event.DecodeData[event.OrderStatusChanged](env)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		case event.OrderCancelled:
			got, err := event.DecodeData[event.OrderCancelled](env)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		}
	}
}

func TestDecodeDataTypeMismatch(t *testing.T) {
	b, _ := event.Encode(event.OrderCancelled{OrderID: orderID, Reason: "r", CancelledBy: "buyer"}, at)
	env, err := event.Decode(b)
	require.NoError(t, err)
	_, err = event.DecodeData[event.OrderCreated](env)
	assert.ErrorIs(t, err, event.ErrTypeMismatch)
}

func TestDecodeRejectsBadEnvelopes(t *testing.T) {
	valid := map[string]any{"event_id": orderID, "event_type": "OrderCancelled", "version": 1,
		"occurred_at": "2026-09-28T10:00:00Z", "data": map[string]any{}}
	mutate := func(k string, v any) []byte {
		m := map[string]any{}
		for key, val := range valid {
			m[key] = val
		}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		return b
	}

	_, err := event.Decode(mutate("version", 2))
	assert.ErrorIs(t, err, event.ErrUnsupportedVersion)
	_, err = event.Decode(mutate("event_id", nil))
	assert.ErrorIs(t, err, event.ErrInvalidEnvelope)
	_, err = event.Decode(mutate("event_type", nil))
	assert.ErrorIs(t, err, event.ErrInvalidEnvelope)
	_, err = event.Decode(mutate("data", nil))
	assert.ErrorIs(t, err, event.ErrInvalidEnvelope)
	_, err = event.Decode([]byte("not json"))
	assert.Error(t, err)
}
```

- [x] **Step 4: Run to verify failure** — `cd pkg && go test ./event/` → FAIL, `undefined: event.OrderCreated`.

- [x] **Step 5: Implement `pkg/event/event.go`**

```go
// Package event defines the Kafka event contract (api/events/*.schema.json):
// the envelope, the order event payloads, and encode/decode helpers.
package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// TopicOrderEvents is the topic for all order events; the message key is the order ID.
const TopicOrderEvents = "order.events"

// Version is the only envelope version defined in P1.
const Version = 1

// Type is the event_type discriminator.
type Type string

const (
	TypeOrderCreated       Type = "OrderCreated"
	TypeOrderStatusChanged Type = "OrderStatusChanged"
	TypeOrderCancelled     Type = "OrderCancelled"
)

// Values of OrderCancelled.CancelledBy.
const (
	CancelledByBuyer  = "buyer"
	CancelledBySeller = "seller"
	CancelledBySystem = "system"
)

var (
	ErrInvalidEnvelope    = errors.New("event: invalid envelope")
	ErrUnsupportedVersion = errors.New("event: unsupported version")
	ErrTypeMismatch       = errors.New("event: type mismatch")
)

// Payload is implemented by every event data struct.
type Payload interface {
	EventType() Type
}

// Envelope wraps every event on the wire.
type Envelope struct {
	EventID    string          `json:"event_id"`
	EventType  Type            `json:"event_type"`
	Version    int             `json:"version"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

type OrderItem struct {
	SkuID     string `json:"sku_id"`
	Quantity  int32  `json:"quantity"`
	UnitPrice int64  `json:"unit_price"`
}

type OrderCreated struct {
	OrderID      string      `json:"order_id"`
	CheckoutID   string      `json:"checkout_id"`
	BuyerID      string      `json:"buyer_id"`
	ShopID       string      `json:"shop_id"`
	SellerUserID string      `json:"seller_user_id"`
	Total        int64       `json:"total"`
	Items        []OrderItem `json:"items"`
}

func (OrderCreated) EventType() Type { return TypeOrderCreated }

type OrderStatusChanged struct {
	OrderID   string `json:"order_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	ActorID   string `json:"actor_id,omitempty"` // empty when ActorRole is "system"
	ActorRole string `json:"actor_role"`
}

func (OrderStatusChanged) EventType() Type { return TypeOrderStatusChanged }

type OrderCancelled struct {
	OrderID     string `json:"order_id"`
	Reason      string `json:"reason"`
	CancelledBy string `json:"cancelled_by"`
}

func (OrderCancelled) EventType() Type { return TypeOrderCancelled }

// New wraps p in an envelope with a fresh UUIDv7 event ID.
func New(p Payload, occurredAt time.Time) (Envelope, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Envelope{}, fmt.Errorf("event: new id: %w", err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		return Envelope{}, fmt.Errorf("event: marshal %s: %w", p.EventType(), err)
	}
	return Envelope{
		EventID:    id.String(),
		EventType:  p.EventType(),
		Version:    Version,
		OccurredAt: occurredAt.UTC().Truncate(time.Millisecond),
		Data:       data,
	}, nil
}

// Encode wraps p in a new envelope and marshals it.
func Encode(p Payload, occurredAt time.Time) ([]byte, error) {
	env, err := New(p, occurredAt)
	if err != nil {
		return nil, err
	}
	return json.Marshal(env)
}

// Decode parses and checks an envelope; the payload stays raw until DecodeData.
func Decode(b []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return Envelope{}, fmt.Errorf("event: decode envelope: %w", err)
	}
	if env.Version != Version {
		return Envelope{}, fmt.Errorf("%w: %d", ErrUnsupportedVersion, env.Version)
	}
	if env.EventID == "" || env.EventType == "" || len(env.Data) == 0 || string(env.Data) == "null" {
		return Envelope{}, ErrInvalidEnvelope
	}
	return env, nil
}

// DecodeData unmarshals the payload of env into T, checking the event type.
func DecodeData[T Payload](env Envelope) (T, error) {
	var out T
	if out.EventType() != env.EventType {
		return out, fmt.Errorf("%w: envelope is %s, want %s", ErrTypeMismatch, env.EventType, out.EventType())
	}
	if err := json.Unmarshal(env.Data, &out); err != nil {
		return out, fmt.Errorf("event: decode %s: %w", env.EventType, err)
	}
	return out, nil
}
```

- [x] **Step 6: Run tests** — `cd pkg && go test ./event/` → PASS.

- [x] **Step 7: Commit**

```bash
git add api/events pkg
git commit -m "feat(contracts): add order event JSON schemas and pkg/event"
```

---

### Task 7: `pkg/otelx`

**Files:**
- Create: `pkg/otelx/otelx.go`, `pkg/otelx/otelx_test.go`

**Interfaces:**
- Produces: `Setup(ctx context.Context, serviceName, endpoint string) (shutdown func(context.Context) error, err error)`; `HTTPHandler(h http.Handler, operation string) http.Handler`; `HTTPTransport(base http.RoundTripper) http.RoundTripper`.

- [x] **Step 1: Add dependencies**

Run: `cd pkg && go get go.opentelemetry.io/otel/sdk@v1.46.0 go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@v1.46.0 go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.71.0`

- [x] **Step 2: Write failing tests** — `pkg/otelx/otelx_test.go`

```go
package otelx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
)

func TestSetupWithoutEndpointStillTracesAndPropagates(t *testing.T) {
	shutdown, err := otelx.Setup(context.Background(), "test-svc", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	ctx, span := otel.Tracer("t").Start(context.Background(), "op")
	defer span.End()
	require.True(t, span.SpanContext().IsValid())

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	assert.Contains(t, carrier["traceparent"], span.SpanContext().TraceID().String())
}

func TestSetupWithEndpoint(t *testing.T) {
	shutdown, err := otelx.Setup(context.Background(), "test-svc", "http://127.0.0.1:4317")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = shutdown(ctx) // nothing listens; only must not hang past the deadline
}

func TestHTTPHandlerContinuesIncomingTrace(t *testing.T) {
	shutdown, err := otelx.Setup(context.Background(), "test-svc", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	var got trace.SpanContext
	h := otelx.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = trace.SpanContextFromContext(r.Context())
	}), "test")

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("traceparent", "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01")
	h.ServeHTTP(httptest.NewRecorder(), r)

	assert.Equal(t, "0102030405060708090a0b0c0d0e0f10", got.TraceID().String())
}

func TestHTTPTransportInjectsTraceparent(t *testing.T) {
	shutdown, err := otelx.Setup(context.Background(), "test-svc", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	var header string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("traceparent")
	}))
	defer srv.Close()

	ctx, span := otel.Tracer("t").Start(context.Background(), "client")
	defer span.End()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := (&http.Client{Transport: otelx.HTTPTransport(nil)}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.Contains(t, header, span.SpanContext().TraceID().String())
}
```

- [x] **Step 3: Run to verify failure** — `cd pkg && go test ./otelx/` → FAIL, `undefined: otelx.Setup`.

- [x] **Step 4: Implement `pkg/otelx/otelx.go`**

```go
// Package otelx configures OpenTelemetry tracing (OTLP gRPC exporter,
// W3C trace-context propagation) and exposes HTTP instrumentation helpers.
package otelx

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Setup installs the global tracer provider and propagator. endpoint is an
// OTLP gRPC URL such as "http://jaeger:4317"; when empty, spans are created
// (so trace IDs still propagate and appear in logs) but not exported.
func Setup(ctx context.Context, serviceName, endpoint string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	res, err := resource.Merge(resource.Default(),
		resource.NewSchemaless(attribute.String("service.name", serviceName)))
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}
	opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	if endpoint != "" {
		exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(endpoint))
		if err != nil {
			return nil, fmt.Errorf("otlp exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	}
	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// HTTPHandler wraps h with a server span per request.
func HTTPHandler(h http.Handler, operation string) http.Handler {
	return otelhttp.NewHandler(h, operation)
}

// HTTPTransport wraps base (http.DefaultTransport when nil) with client spans
// and traceparent injection.
func HTTPTransport(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base)
}
```

- [x] **Step 5: Run tests** — `cd pkg && go test ./otelx/` → PASS. If `resource.Merge` returns a schema-URL conflict error, replace `resource.Default()` with `resource.Empty()` and rerun.

- [x] **Step 6: Commit**

```bash
git add pkg
git commit -m "feat(pkg/otelx): add OpenTelemetry setup and HTTP instrumentation"
```

---

### Task 8: `pkg/server`

**Files:**
- Create: `pkg/server/health.go`, `pkg/server/run.go`, `pkg/server/health_test.go`, `pkg/server/run_test.go`

**Interfaces:**
- Consumes: `httpx.WriteJSON`.
- Produces: `type Check func(ctx context.Context) error`; `Healthz() http.Handler`; `Readyz(checks map[string]Check) http.Handler`; `const ReadyTimeout = 2 * time.Second`; `Probe(url string) error`; `ReadyURL(httpAddr string) string`; `HealthcheckCommand(httpAddr string) int`; `type Config struct{HTTP *http.Server; GRPC *grpc.Server; GRPCAddr string; ShutdownTimeout time.Duration; Logger *slog.Logger}`; `Run(ctx context.Context, cfg Config) error`.

- [x] **Step 1: Add dependency** — `cd pkg && go get google.golang.org/grpc@v1.84.0`

- [x] **Step 2: Write failing health tests** — `pkg/server/health_test.go`

```go
package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

func get(h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := get(server.Healthz())
	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}

func TestReadyzAllOK(t *testing.T) {
	ok := func(context.Context) error { return nil }
	rec := get(server.Readyz(map[string]server.Check{"postgres": ok, "kafka": ok}))
	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"status":"ok","checks":{"postgres":"ok","kafka":"ok"}}`, rec.Body.String())
}

func TestReadyzNoChecks(t *testing.T) {
	rec := get(server.Readyz(nil))
	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"status":"ok","checks":{}}`, rec.Body.String())
}

func TestReadyzFailingCheck(t *testing.T) {
	rec := get(server.Readyz(map[string]server.Check{
		"postgres": func(context.Context) error { return nil },
		"redis":    func(context.Context) error { return errors.New("dial tcp: refused") },
	}))
	assert.Equal(t, 503, rec.Code)
	assert.JSONEq(t, `{"status":"unavailable","checks":{"postgres":"ok","redis":"unavailable"}}`, rec.Body.String())
}

func TestReadyzHangingCheckTimesOut(t *testing.T) {
	start := time.Now()
	rec := get(server.Readyz(map[string]server.Check{
		"slow": func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
	}))
	assert.Equal(t, 503, rec.Code)
	assert.Less(t, time.Since(start), server.ReadyTimeout+time.Second)
}

func TestReadyURL(t *testing.T) {
	assert.Equal(t, "http://127.0.0.1:8081/readyz", server.ReadyURL(":8081"))
	assert.Equal(t, "http://127.0.0.1:8080/readyz", server.ReadyURL(""))
	assert.Equal(t, "http://127.0.0.1:9000/readyz", server.ReadyURL("0.0.0.0:9000"))
}

func TestProbe(t *testing.T) {
	up := httptest.NewServer(server.Healthz())
	defer up.Close()
	assert.NoError(t, server.Probe(up.URL))

	down := httptest.NewServer(server.Readyz(map[string]server.Check{"x": func(context.Context) error { return errors.New("no") }}))
	defer down.Close()
	assert.Error(t, server.Probe(down.URL))

	assert.Error(t, server.Probe("http://127.0.0.1:1/readyz"))
}
```

- [x] **Step 3: Write failing Run tests** — `pkg/server/run_test.go`

```go
package server_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func TestRunServesAndShutsDownGracefully(t *testing.T) {
	httpAddr, grpcAddr := freeAddr(t), freeAddr(t)
	grpcSrv := grpc.NewServer()
	healthpb.RegisterHealthServer(grpcSrv, health.NewServer())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, server.Config{
			HTTP:            &http.Server{Addr: httpAddr, Handler: server.Healthz(), ReadHeaderTimeout: time.Second},
			GRPC:            grpcSrv,
			GRPCAddr:        grpcAddr,
			ShutdownTimeout: 2 * time.Second,
			Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}()

	require.Eventually(t, func() bool { return server.Probe("http://"+httpAddr) == nil }, 3*time.Second, 20*time.Millisecond)

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	resp, err := healthpb.NewHealthClient(conn).Check(context.Background(), &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus())

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	assert.Error(t, server.Probe("http://"+httpAddr))
}

func TestRunReturnsListenError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	err = server.Run(context.Background(), server.Config{
		HTTP:   &http.Server{Addr: l.Addr().String(), ReadHeaderTimeout: time.Second},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	assert.ErrorContains(t, err, "listen http")
}
```

- [x] **Step 4: Run to verify failure** — `cd pkg && go test ./server/` → FAIL, `undefined: server.Healthz`.

- [x] **Step 5: Implement `pkg/server/health.go`**

```go
// Package server runs a service's HTTP and gRPC servers with graceful
// shutdown and provides the /healthz and /readyz handlers.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

// ReadyTimeout bounds how long all readiness checks together may take.
const ReadyTimeout = 2 * time.Second

// Check reports whether a dependency is usable.
type Check func(ctx context.Context) error

// Healthz is the liveness handler: 200 while the process is running.
func Healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

type readyBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Readyz runs every check concurrently and responds 200 when all pass, 503 otherwise.
func Readyz(checks map[string]Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), ReadyTimeout)
		defer cancel()

		body := readyBody{Status: "ok", Checks: make(map[string]string, len(checks))}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for name, check := range checks {
			wg.Go(func() {
				err := check(ctx)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					slog.WarnContext(r.Context(), "readiness check failed", "check", name, "error", err)
					body.Checks[name] = "unavailable"
					body.Status = "unavailable"
					return
				}
				body.Checks[name] = "ok"
			})
		}
		wg.Wait()

		status := http.StatusOK
		if body.Status != "ok" {
			status = http.StatusServiceUnavailable
		}
		httpx.WriteJSON(w, status, body)
	})
}

// ReadyURL turns a listen address such as ":8081" into the local /readyz URL.
func ReadyURL(httpAddr string) string {
	if httpAddr == "" {
		httpAddr = ":8080"
	}
	_, port, err := net.SplitHostPort(httpAddr)
	if err != nil {
		port = "8080"
	}
	return "http://127.0.0.1:" + port + "/readyz"
}

// Probe GETs url and returns nil on a 2xx response.
func Probe(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("probe %s: status %d", url, resp.StatusCode)
	}
	return nil
}

// HealthcheckCommand implements `server healthcheck` for distroless container
// healthchecks: it probes the local /readyz and returns the process exit code.
func HealthcheckCommand(httpAddr string) int {
	if err := Probe(ReadyURL(httpAddr)); err != nil {
		fmt.Println(err)
		return 1
	}
	return 0
}
```

- [x] **Step 6: Implement `pkg/server/run.go`**

```go
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"
)

// Config describes what Run serves. GRPC may be nil for HTTP-only services.
type Config struct {
	HTTP            *http.Server
	GRPC            *grpc.Server
	GRPCAddr        string
	ShutdownTimeout time.Duration
	Logger          *slog.Logger
}

// Run serves HTTP (and gRPC when configured) until ctx is cancelled or a
// server fails, then shuts both down gracefully within ShutdownTimeout.
func Run(ctx context.Context, cfg Config) error {
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 15 * time.Second
	}
	var lc net.ListenConfig
	httpLn, err := lc.Listen(ctx, "tcp", cfg.HTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen http %s: %w", cfg.HTTP.Addr, err)
	}
	errCh := make(chan error, 2)
	go func() {
		if err := cfg.HTTP.Serve(httpLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()
	cfg.Logger.Info("http server listening", "addr", httpLn.Addr().String())

	if cfg.GRPC != nil {
		grpcLn, err := lc.Listen(ctx, "tcp", cfg.GRPCAddr)
		if err != nil {
			_ = cfg.HTTP.Close()
			return fmt.Errorf("listen grpc %s: %w", cfg.GRPCAddr, err)
		}
		go func() {
			if err := cfg.GRPC.Serve(grpcLn); err != nil {
				errCh <- fmt.Errorf("grpc server: %w", err)
			}
		}()
		cfg.Logger.Info("grpc server listening", "addr", grpcLn.Addr().String())
	}

	var runErr error
	select {
	case <-ctx.Done():
		cfg.Logger.Info("shutting down")
	case runErr = <-errCh:
		cfg.Logger.Error("server failed, shutting down", "error", runErr)
	}

	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := cfg.HTTP.Shutdown(sctx); err != nil {
		cfg.Logger.Error("http shutdown", "error", err)
	}
	if cfg.GRPC != nil {
		stopped := make(chan struct{})
		go func() { cfg.GRPC.GracefulStop(); close(stopped) }()
		select {
		case <-stopped:
		case <-sctx.Done():
			cfg.GRPC.Stop()
		}
	}
	return runErr
}
```

- [x] **Step 7: Run tests** — `cd pkg && go test -race ./server/` → PASS.

- [x] **Step 8: Commit**

```bash
git add pkg
git commit -m "feat(pkg/server): add health handlers and graceful HTTP/gRPC runner"
```

---

### Task 9: `pkg/grpcx`

**Files:**
- Create: `pkg/grpcx/grpcx.go`, `pkg/grpcx/grpcx_test.go`

**Interfaces:**
- Produces: `const DefaultTimeout = 5 * time.Second`; `NewServer(opts ...grpc.ServerOption) (*grpc.Server, *health.Server)`; `Dial(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error)`; `DefaultTimeoutInterceptor(d time.Duration) grpc.UnaryClientInterceptor`.

- [x] **Step 1: Add dependency** — `cd pkg && go get go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc@v0.71.0`

- [x] **Step 2: Write failing tests** — `pkg/grpcx/grpcx_test.go`

```go
package grpcx_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
)

func startBufconn(t *testing.T, opts ...grpc.ServerOption) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv, _ := grpcx.NewServer(opts...)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpcx.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestServerExposesHealth(t *testing.T) {
	conn := startBufconn(t)
	resp, err := healthpb.NewHealthClient(conn).Check(context.Background(), &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus())
}

func TestServerRecoversFromPanics(t *testing.T) {
	conn := startBufconn(t, grpc.UnknownServiceHandler(func(any, grpc.ServerStream) error {
		panic("boom")
	}))
	err := conn.Invoke(context.Background(), "/test.v1.Nope/Call", &emptypb.Empty{}, &emptypb.Empty{})
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestDefaultTimeoutInterceptorAddsDeadline(t *testing.T) {
	icpt := grpcx.DefaultTimeoutInterceptor(time.Second)
	var deadline time.Time
	var ok bool
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		deadline, ok = ctx.Deadline()
		return nil
	}

	require.NoError(t, icpt(context.Background(), "/m", nil, nil, nil, invoker))
	assert.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(time.Second), deadline, 200*time.Millisecond)
}

func TestDefaultTimeoutInterceptorKeepsCallerDeadline(t *testing.T) {
	icpt := grpcx.DefaultTimeoutInterceptor(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	want, _ := ctx.Deadline()

	var got time.Time
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		got, _ = ctx.Deadline()
		return nil
	}
	require.NoError(t, icpt(ctx, "/m", nil, nil, nil, invoker))
	assert.Equal(t, want, got)
}
```

- [x] **Step 3: Run to verify failure** — `cd pkg && go test ./grpcx/` → FAIL, `undefined: grpcx.NewServer`.

- [x] **Step 4: Implement `pkg/grpcx/grpcx.go`**

```go
// Package grpcx builds gRPC servers and clients with the project defaults:
// OpenTelemetry instrumentation, panic recovery, health service and a default
// client deadline.
package grpcx

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// DefaultTimeout applies to client calls whose context has no deadline.
const DefaultTimeout = 5 * time.Second

// NewServer returns a server with tracing, panic recovery and a registered
// health service (reported SERVING for the overall server).
func NewServer(opts ...grpc.ServerOption) (*grpc.Server, *health.Server) {
	base := []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(recoverUnary),
		grpc.ChainStreamInterceptor(recoverStream),
	}
	srv := grpc.NewServer(append(base, opts...)...)
	h := health.NewServer()
	healthpb.RegisterHealthServer(srv, h)
	return srv, h
}

// Dial creates a plaintext client connection (internal network) with tracing
// and DefaultTimeout.
func Dial(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	base := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithChainUnaryInterceptor(DefaultTimeoutInterceptor(DefaultTimeout)),
	}
	return grpc.NewClient(target, append(base, opts...)...)
}

// DefaultTimeoutInterceptor sets a deadline of d on calls that have none.
func DefaultTimeoutInterceptor(d time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func recoverUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	defer func() {
		if v := recover(); v != nil {
			slog.ErrorContext(ctx, "grpc panic recovered", "method", info.FullMethod, "panic", v, "stack", string(debug.Stack()))
			err = status.Error(codes.Internal, "internal error")
		}
	}()
	return handler(ctx, req)
}

func recoverStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if v := recover(); v != nil {
			slog.ErrorContext(ss.Context(), "grpc panic recovered", "method", info.FullMethod, "panic", v, "stack", string(debug.Stack()))
			err = status.Error(codes.Internal, "internal error")
		}
	}()
	return handler(srv, ss)
}
```

- [x] **Step 5: Run tests** — `cd pkg && go test -race ./grpcx/` → PASS.

- [x] **Step 6: Commit**

```bash
git add pkg
git commit -m "feat(pkg/grpcx): add instrumented gRPC server and client constructors"
```

---

### Task 10: `pkg/testutil` containers + `pkg/pgxx`

**Files:**
- Create: `pkg/testutil/containers.go`, `pkg/testutil/containers_test.go`, `pkg/pgxx/pgxx.go`, `pkg/pgxx/pgxx_test.go`

**Interfaces:**
- Produces (`testutil`): image constants `PostgresImage = "postgres:17-alpine"`, `RedisImage = "redis:7-alpine"`, `KafkaImage = "confluentinc/confluent-local:7.9.10"`, `MinIOImage = "pgsty/minio:RELEASE.2026-08-04T00-00-00Z"`; `Postgres(t testing.TB) string` (DSN); `Redis(t testing.TB) string` (`redis://host:port`); `Kafka(t testing.TB) []string`; `CreateTopic(t testing.TB, brokers []string, topic string, partitions int32)`; `type MinIOInfo struct{Endpoint, AccessKey, SecretKey string}`; `MinIO(t testing.TB) MinIOInfo`. All skip under `go test -short`.
- Produces (`pgxx`): `NewPool(ctx, url string) (*pgxpool.Pool, error)`; `Migrate(databaseURL string, migrations fs.FS) error`; `WithTx(ctx, pool *pgxpool.Pool, fn func(pgx.Tx) error) error`.

- [x] **Step 1: Add dependencies**

Run: `cd pkg && go get github.com/testcontainers/testcontainers-go@v0.44.0 github.com/testcontainers/testcontainers-go/modules/postgres@v0.44.0 github.com/testcontainers/testcontainers-go/modules/redis@v0.44.0 github.com/testcontainers/testcontainers-go/modules/kafka@v0.44.0 github.com/testcontainers/testcontainers-go/modules/minio@v0.44.0 github.com/twmb/franz-go@v1.22.1 github.com/twmb/franz-go/pkg/kadm@v1.19.0 github.com/jackc/pgx/v5@v5.11.0 github.com/golang-migrate/migrate/v4@v4.20.1`

- [x] **Step 2: Write failing container tests** — `pkg/testutil/containers_test.go` (Postgres and Kafka are exercised by the pgxx/kafkax/outbox tests)

```go
package testutil_test

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

func TestRedisAnswersPing(t *testing.T) {
	url := testutil.Redis(t)
	require.True(t, strings.HasPrefix(url, "redis://"))

	var d net.Dialer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", strings.TrimPrefix(url, "redis://"))
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("PING\r\n"))
	require.NoError(t, err)
	line, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "+PONG\r\n", line)
}

func TestMinIOIsLive(t *testing.T) {
	info := testutil.MinIO(t)
	assert.NotEmpty(t, info.AccessKey)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+info.Endpoint+"/minio/health/live", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
```

- [x] **Step 3: Run to verify failure** — `cd pkg && go test ./testutil/` → FAIL, `undefined: testutil.Redis`.

- [x] **Step 4: Implement `pkg/testutil/containers.go`**

```go
// Package testutil starts throwaway infrastructure for integration tests
// (testcontainers) and validates HTTP traffic against the OpenAPI contracts.
// Container helpers skip the test under `go test -short`.
package testutil

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	PostgresImage = "postgres:17-alpine"
	RedisImage    = "redis:7-alpine"
	KafkaImage    = "confluentinc/confluent-local:7.9.10"
	MinIOImage    = "pgsty/minio:RELEASE.2026-08-04T00-00-00Z"
)

func skipShort(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("container test skipped in -short mode")
	}
}

// Postgres starts PostgreSQL and returns a DSN for database "test".
func Postgres(t testing.TB) string {
	skipShort(t)
	ctx := context.Background()
	c, err := postgres.Run(ctx, PostgresImage,
		postgres.WithDatabase("test"), postgres.WithUsername("test"), postgres.WithPassword("test"),
		postgres.BasicWaitStrategies())
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres dsn: %v", err)
	}
	return dsn
}

// Redis starts Redis and returns its URL (redis://host:port).
func Redis(t testing.TB) string {
	skipShort(t)
	ctx := context.Background()
	c, err := redis.Run(ctx, RedisImage)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start redis: %v", err)
	}
	url, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("redis url: %v", err)
	}
	return url
}

// Kafka starts a single-node Kafka and returns its bootstrap brokers.
func Kafka(t testing.TB) []string {
	skipShort(t)
	ctx := context.Background()
	c, err := kafka.Run(ctx, KafkaImage, kafka.WithClusterID("test-cluster"))
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start kafka: %v", err)
	}
	brokers, err := c.Brokers(ctx)
	if err != nil {
		t.Fatalf("kafka brokers: %v", err)
	}
	return brokers
}

// CreateTopic creates topic with the given partition count (replication factor 1).
func CreateTopic(t testing.TB, brokers []string, topic string, partitions int32) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatalf("kafka client: %v", err)
	}
	defer cl.Close()
	resp, err := kadm.NewClient(cl).CreateTopic(context.Background(), partitions, 1, nil, topic)
	if err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	if resp.Err != nil {
		t.Fatalf("create topic %s: %v", topic, resp.Err)
	}
}

// MinIOInfo describes a running MinIO.
type MinIOInfo struct {
	Endpoint  string // host:port
	AccessKey string
	SecretKey string
}

// MinIO starts MinIO and returns its endpoint and credentials.
func MinIO(t testing.TB) MinIOInfo {
	skipShort(t)
	ctx := context.Background()
	c, err := minio.Run(ctx, MinIOImage, minio.WithUsername("minioadmin"), minio.WithPassword("minioadmin"))
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	endpoint, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("minio endpoint: %v", err)
	}
	return MinIOInfo{Endpoint: endpoint, AccessKey: c.Username, SecretKey: c.Password}
}
```

- [x] **Step 5: Run container tests** — requires Docker running. `cd pkg && go test ./testutil/` → PASS.

- [x] **Step 6: Write failing pgxx tests** — `pkg/pgxx/pgxx_test.go`

```go
package pgxx_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

var migrations = fstest.MapFS{
	"000001_items.up.sql":   {Data: []byte(`CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL);`)},
	"000001_items.down.sql": {Data: []byte(`DROP TABLE items;`)},
}

func TestMigrateAndTransactions(t *testing.T) {
	dsn := testutil.Postgres(t)
	ctx := context.Background()

	require.NoError(t, pgxx.Migrate(dsn, migrations))
	require.NoError(t, pgxx.Migrate(dsn, migrations), "second run is a no-op")

	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	count := func() int {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM items`).Scan(&n))
		return n
	}

	t.Run("commit", func(t *testing.T) {
		err := pgxx.WithTx(ctx, pool, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO items VALUES (1, 'a')`)
			return err
		})
		require.NoError(t, err)
		assert.Equal(t, 1, count())
	})

	t.Run("rollback on error", func(t *testing.T) {
		boom := errors.New("boom")
		err := pgxx.WithTx(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO items VALUES (2, 'b')`); err != nil {
				return err
			}
			return boom
		})
		assert.ErrorIs(t, err, boom)
		assert.Equal(t, 1, count())
	})

	t.Run("rollback on panic", func(t *testing.T) {
		assert.Panics(t, func() {
			_ = pgxx.WithTx(ctx, pool, func(tx pgx.Tx) error {
				_, _ = tx.Exec(ctx, `INSERT INTO items VALUES (3, 'c')`)
				panic("boom")
			})
		})
		assert.Equal(t, 1, count())
	})
}

func TestNewPoolFailsFastOnBadURL(t *testing.T) {
	_, err := pgxx.NewPool(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	assert.Error(t, err)
}
```

- [x] **Step 7: Run to verify failure** — `cd pkg && go test ./pgxx/` → FAIL, `undefined: pgxx.Migrate`.

- [x] **Step 8: Implement `pkg/pgxx/pgxx.go`**

```go
// Package pgxx wraps pgx pool creation, embedded golang-migrate migrations
// and transactions.
package pgxx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5:// driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool connects to url and verifies the connection with a ping.
func NewPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies all pending up-migrations found at the root of migrations
// (golang-migrate naming: 000001_name.up.sql / .down.sql).
func Migrate(databaseURL string, migrations fs.FS) error {
	src, err := iofs.New(migrations, ".")
	if err != nil {
		return fmt.Errorf("open migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, pgx5URL(databaseURL))
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func pgx5URL(u string) string {
	for _, prefix := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(u, prefix) {
			return "pgx5://" + strings.TrimPrefix(u, prefix)
		}
	}
	return u
}

// WithTx runs fn in a transaction, committing on nil and rolling back on
// error or panic.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
```

- [x] **Step 9: Run tests** — `cd pkg && go test ./pgxx/ ./testutil/` → PASS.

- [x] **Step 10: Commit**

```bash
git add pkg
git commit -m "feat(pkg): add testcontainers helpers and pgxx pool/migrate/tx"
```

---

### Task 11: `pkg/kafkax`

**Files:**
- Create: `pkg/kafkax/producer.go`, `pkg/kafkax/consumer.go`, `pkg/kafkax/kafkax_test.go`

**Interfaces:**
- Produces: `type Message struct{Topic string; Key, Value []byte; Headers map[string]string}`; `NewProducer(brokers []string, opts ...kgo.Opt) (*Producer, error)`; `(*Producer) Publish(ctx, msgs ...Message) error`; `(*Producer) Ping(ctx) error`; `(*Producer) Close()`; `type Handler func(ctx context.Context, m Message) error`; `type ConsumerConfig struct{Brokers []string; Group string; Topics []string; MinBackoff, MaxBackoff time.Duration; Logger *slog.Logger}`; `NewConsumer(cfg ConsumerConfig, h Handler, opts ...kgo.Opt) (*Consumer, error)`; `(*Consumer) Run(ctx) error`; `(*Consumer) Close()`.

- [x] **Step 1: Write failing tests** — `pkg/kafkax/kafkax_test.go`

```go
package kafkax_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func runConsumer(t *testing.T, brokers []string, topic string, h kafkax.Handler) (stop func()) {
	t.Helper()
	c, err := kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: brokers, Group: "test-group", Topics: []string{topic},
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond, Logger: discard,
	}, h)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	return func() { cancel(); <-done; c.Close() }
}

func TestProduceConsumeRetryAndCommit(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	brokers := testutil.Kafka(t)
	const topic = "kafkax-test"
	testutil.CreateTopic(t, brokers, topic, 1)

	p, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	defer p.Close()
	require.NoError(t, p.Ping(context.Background()))

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	pctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled, Remote: true,
	}))
	require.NoError(t, p.Publish(pctx, kafkax.Message{
		Topic: topic, Key: []byte("order-1"), Value: []byte(`{"n":1}`), Headers: map[string]string{"x-test": "yes"},
	}))

	// First consumer: fail twice, then succeed.
	var calls atomic.Int32
	var mu sync.Mutex
	var got kafkax.Message
	var gotTrace trace.TraceID
	processed := make(chan struct{})
	stop := runConsumer(t, brokers, topic, func(ctx context.Context, m kafkax.Message) error {
		if calls.Add(1) < 3 {
			return errors.New("transient")
		}
		mu.Lock()
		got, gotTrace = m, trace.SpanContextFromContext(ctx).TraceID()
		mu.Unlock()
		close(processed)
		return nil
	})
	select {
	case <-processed:
	case <-time.After(30 * time.Second):
		t.Fatal("message not processed")
	}
	time.Sleep(500 * time.Millisecond) // let the commit land
	stop()

	assert.EqualValues(t, 3, calls.Load())
	mu.Lock()
	assert.Equal(t, "order-1", string(got.Key))
	assert.JSONEq(t, `{"n":1}`, string(got.Value))
	assert.Equal(t, "yes", got.Headers["x-test"])
	assert.Contains(t, got.Headers["traceparent"], tid.String())
	assert.Equal(t, tid, gotTrace)
	mu.Unlock()

	// Second consumer in the same group must only see the new message.
	require.NoError(t, p.Publish(context.Background(), kafkax.Message{Topic: topic, Key: []byte("order-2"), Value: []byte(`{"n":2}`)}))
	keys := make(chan string, 10)
	stop2 := runConsumer(t, brokers, topic, func(_ context.Context, m kafkax.Message) error {
		keys <- string(m.Key)
		return nil
	})
	defer stop2()
	select {
	case k := <-keys:
		assert.Equal(t, "order-2", k)
	case <-time.After(30 * time.Second):
		t.Fatal("second message not consumed")
	}
}

func TestUncommittedMessageIsRedelivered(t *testing.T) {
	brokers := testutil.Kafka(t)
	const topic = "kafkax-redeliver"
	testutil.CreateTopic(t, brokers, topic, 1)

	p, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	defer p.Close()
	require.NoError(t, p.Publish(context.Background(), kafkax.Message{Topic: topic, Key: []byte("k"), Value: []byte(`{}`)}))

	attempted := make(chan struct{}, 100)
	stop := runConsumer(t, brokers, topic, func(context.Context, kafkax.Message) error {
		attempted <- struct{}{}
		return errors.New("always failing")
	})
	<-attempted
	<-attempted // retried, not skipped
	stop()

	redelivered := make(chan string, 1)
	stop2 := runConsumer(t, brokers, topic, func(_ context.Context, m kafkax.Message) error {
		select {
		case redelivered <- string(m.Key):
		default:
		}
		return nil
	})
	defer stop2()
	select {
	case k := <-redelivered:
		assert.Equal(t, "k", k)
	case <-time.After(30 * time.Second):
		t.Fatal("failed message was not redelivered")
	}
}
```

- [x] **Step 2: Run to verify failure** — `cd pkg && go test ./kafkax/` → FAIL, `undefined: kafkax.NewProducer`.

- [x] **Step 3: Implement `pkg/kafkax/producer.go`**

```go
// Package kafkax wraps franz-go with the project conventions: synchronous
// publishing with trace propagation, and consumer groups that commit offsets
// only after successful processing, retrying failures with backoff.
package kafkax

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// Message is a Kafka record in transport-neutral form.
type Message struct {
	Topic   string
	Key     []byte
	Value   []byte
	Headers map[string]string
}

// Producer publishes messages synchronously (acks=all).
type Producer struct {
	client *kgo.Client
}

// NewProducer connects to brokers. Extra kgo options are appended.
func NewProducer(brokers []string, opts ...kgo.Opt) (*Producer, error) {
	base := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerLinger(5 * time.Millisecond),
	}
	cl, err := kgo.NewClient(append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}
	return &Producer{client: cl}, nil
}

// Publish sends msgs and waits for every ack. A traceparent from ctx is added
// to messages that do not already carry one.
func (p *Producer) Publish(ctx context.Context, msgs ...Message) error {
	recs := make([]*kgo.Record, 0, len(msgs))
	for _, m := range msgs {
		h := maps.Clone(m.Headers)
		if h == nil {
			h = map[string]string{}
		}
		if _, ok := h["traceparent"]; !ok {
			otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(h))
		}
		recs = append(recs, &kgo.Record{Topic: m.Topic, Key: m.Key, Value: m.Value, Headers: toKgoHeaders(h)})
	}
	if err := p.client.ProduceSync(ctx, recs...).FirstErr(); err != nil {
		return fmt.Errorf("kafka publish: %w", err)
	}
	return nil
}

// Ping checks broker connectivity (for /readyz).
func (p *Producer) Ping(ctx context.Context) error { return p.client.Ping(ctx) }

// Close flushes and closes the client.
func (p *Producer) Close() { p.client.Close() }

func toKgoHeaders(h map[string]string) []kgo.RecordHeader {
	out := make([]kgo.RecordHeader, 0, len(h))
	for k, v := range h {
		out = append(out, kgo.RecordHeader{Key: k, Value: []byte(v)})
	}
	return out
}

func fromKgoHeaders(hs []kgo.RecordHeader) map[string]string {
	out := make(map[string]string, len(hs))
	for _, h := range hs {
		out[h.Key] = string(h.Value)
	}
	return out
}
```

- [x] **Step 4: Implement `pkg/kafkax/consumer.go`**

```go
package kafkax

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Handler processes one message. Returning an error retries the same message.
type Handler func(ctx context.Context, m Message) error

// ConsumerConfig configures a consumer group member.
type ConsumerConfig struct {
	Brokers    []string
	Group      string
	Topics     []string
	MinBackoff time.Duration // default 200ms
	MaxBackoff time.Duration // default 10s
	Logger     *slog.Logger
}

// Consumer processes records one at a time, in partition order.
type Consumer struct {
	client  *kgo.Client
	handler Handler
	cfg     ConsumerConfig
}

// NewConsumer joins cfg.Group. New groups start from the earliest offset.
func NewConsumer(cfg ConsumerConfig, h Handler, opts ...kgo.Opt) (*Consumer, error) {
	if cfg.MinBackoff == 0 {
		cfg.MinBackoff = 200 * time.Millisecond
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	base := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Topics...),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	}
	cl, err := kgo.NewClient(append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer: %w", err)
	}
	return &Consumer{client: cl, handler: h, cfg: cfg}, nil
}

// Run polls until ctx is cancelled. Each record is retried with exponential
// backoff until it succeeds; offsets of a polled batch are committed only
// after every record in it was handled (at-least-once delivery).
func (c *Consumer) Run(ctx context.Context) error {
	for {
		fetches := c.client.PollFetches(ctx)
		if ctx.Err() != nil {
			c.client.AllowRebalance()
			return nil
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			c.cfg.Logger.ErrorContext(ctx, "kafka fetch error", "topic", topic, "partition", partition, "error", err)
		})

		aborted := false
		fetches.EachRecord(func(r *kgo.Record) {
			if aborted {
				return
			}
			if err := c.handle(ctx, r); err != nil {
				aborted = true // ctx cancelled mid-retry: leave the batch uncommitted
			}
		})
		if aborted {
			c.client.AllowRebalance()
			return nil
		}
		if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
			c.cfg.Logger.ErrorContext(ctx, "kafka commit failed", "error", err)
		}
		c.client.AllowRebalance()
	}
}

func (c *Consumer) handle(ctx context.Context, r *kgo.Record) error {
	msg := Message{Topic: r.Topic, Key: r.Key, Value: r.Value, Headers: fromKgoHeaders(r.Headers)}
	mctx := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(msg.Headers))
	mctx, span := otel.Tracer("kafkax").Start(mctx, "consume "+r.Topic, trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()

	backoff := c.cfg.MinBackoff
	for {
		err := c.handler(mctx, msg)
		if err == nil {
			return nil
		}
		c.cfg.Logger.WarnContext(mctx, "kafka handler failed, retrying",
			"topic", r.Topic, "partition", r.Partition, "offset", r.Offset, "backoff", backoff, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, c.cfg.MaxBackoff)
	}
}

// Close leaves the group and closes the client.
func (c *Consumer) Close() { c.client.Close() }
```

- [x] **Step 5: Run tests** — `cd pkg && go test -race ./kafkax/` → PASS.

- [x] **Step 6: Commit**

```bash
git add pkg/kafkax
git commit -m "feat(pkg/kafkax): add producer and at-least-once consumer with trace propagation"
```

---

### Task 12: `pkg/outbox`

**Files:**
- Create: `pkg/outbox/schema.sql`, `pkg/outbox/outbox.go`, `pkg/outbox/relay.go`, `pkg/outbox/outbox_test.go`

**Interfaces:**
- Consumes: `kafkax.Message`, `kafkax.Producer` (satisfies `Publisher`), `pgxx.WithTx`, `testutil.Postgres/Kafka/CreateTopic`.
- Produces: `var SchemaSQL string` (embedded `schema.sql`; services copy it into a migration); `type Message struct{Topic, Key string; Payload []byte; Headers map[string]string}`; `type Execer interface{ Exec(ctx, sql string, args ...any) (pgconn.CommandTag, error) }`; `Write(ctx, tx Execer, m Message) error`; `type Publisher interface{ Publish(ctx, msgs ...kafkax.Message) error }`; `type RelayConfig struct{BatchSize int; Interval time.Duration; Logger *slog.Logger}` (defaults 100, 500ms); `NewRelay(pool *pgxpool.Pool, pub Publisher, cfg RelayConfig) *Relay`; `(*Relay) ProcessBatch(ctx) (int, error)`; `(*Relay) Run(ctx) error`.

- [x] **Step 1: Write `pkg/outbox/schema.sql`**

```sql
-- Transactional outbox table (pkg/outbox). Services that publish events copy
-- this into one of their own migrations.
CREATE TABLE IF NOT EXISTS outbox (
    id           uuid        PRIMARY KEY,
    topic        text        NOT NULL,
    msg_key      text        NOT NULL,
    payload      jsonb       NOT NULL,
    headers      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz NULL
);

CREATE INDEX IF NOT EXISTS outbox_unpublished_idx
    ON outbox (created_at, id)
    WHERE published_at IS NULL;
```

- [x] **Step 2: Write failing tests** — `pkg/outbox/outbox_test.go`

```go
package outbox_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/outbox"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, testutil.Postgres(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, outbox.SchemaSQL)
	require.NoError(t, err)
	return pool
}

func write(t *testing.T, pool *pgxpool.Pool, ctx context.Context, m outbox.Message, fail bool) {
	t.Helper()
	err := pgxx.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if err := outbox.Write(ctx, tx, m); err != nil {
			return err
		}
		if fail {
			return errors.New("business failure")
		}
		return nil
	})
	if !fail {
		require.NoError(t, err)
	}
}

func consumeAll(t *testing.T, brokers []string, topic string, wait time.Duration) []*kgo.Record {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	require.NoError(t, err)
	defer cl.Close()
	var recs []*kgo.Record
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		cl.PollFetches(ctx).EachRecord(func(r *kgo.Record) { recs = append(recs, r) })
		cancel()
	}
	return recs
}

func TestRolledBackTxPublishesNothingAndCommittedTxPublishesOnce(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	ctx := context.Background()
	pool := newPool(t)
	brokers := testutil.Kafka(t)
	const topic = "outbox-test"
	testutil.CreateTopic(t, brokers, topic, 3)

	producer, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	defer producer.Close()
	relay := outbox.NewRelay(pool, producer, outbox.RelayConfig{Logger: discard})

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	reqCtx := trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled,
	}))

	write(t, pool, reqCtx, outbox.Message{Topic: topic, Key: "rolled-back", Payload: []byte(`{"n":0}`)}, true)
	write(t, pool, reqCtx, outbox.Message{Topic: topic, Key: "committed", Payload: []byte(`{"n":1}`)}, false)

	n, err := relay.ProcessBatch(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	n, err = relay.ProcessBatch(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "already published rows are not sent again")

	recs := consumeAll(t, brokers, topic, 5*time.Second)
	require.Len(t, recs, 1)
	assert.Equal(t, "committed", string(recs[0].Key))
	assert.JSONEq(t, `{"n":1}`, string(recs[0].Value))
	var traceparent string
	for _, h := range recs[0].Headers {
		if h.Key == "traceparent" {
			traceparent = string(h.Value)
		}
	}
	assert.Contains(t, traceparent, tid.String(), "trace context of the writing request is carried")
}

type failingPublisher struct{ calls int }

func (f *failingPublisher) Publish(context.Context, ...kafkax.Message) error {
	f.calls++
	return errors.New("kafka down")
}

type recordingPublisher struct{ msgs []kafkax.Message }

func (r *recordingPublisher) Publish(_ context.Context, msgs ...kafkax.Message) error {
	r.msgs = append(r.msgs, msgs...)
	return nil
}

func TestPublishFailureKeepsRowsForRetry(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	write(t, pool, ctx, outbox.Message{Topic: "t", Key: "k", Payload: []byte(`{}`)}, false)

	failing := &failingPublisher{}
	n, err := outbox.NewRelay(pool, failing, outbox.RelayConfig{Logger: discard}).ProcessBatch(ctx)
	assert.Error(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, 1, failing.calls)

	var unpublished int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&unpublished))
	assert.Equal(t, 1, unpublished)

	ok := &recordingPublisher{}
	n, err = outbox.NewRelay(pool, ok, outbox.RelayConfig{Logger: discard}).ProcessBatch(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.Len(t, ok.msgs, 1)
	assert.Equal(t, "k", string(ok.msgs[0].Key))
}

func TestBatchSizeAndOrder(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	for _, k := range []string{"a", "b", "c"} {
		write(t, pool, ctx, outbox.Message{Topic: "t", Key: k, Payload: []byte(`{}`)}, false)
	}
	rec := &recordingPublisher{}
	relay := outbox.NewRelay(pool, rec, outbox.RelayConfig{BatchSize: 2, Logger: discard})

	n, err := relay.ProcessBatch(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	n, err = relay.ProcessBatch(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	var keys []string
	for _, m := range rec.msgs {
		keys = append(keys, string(m.Key))
	}
	assert.Equal(t, []string{"a", "b", "c"}, keys)
}

func TestRunDrainsUntilCancelled(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	write(t, pool, ctx, outbox.Message{Topic: "t", Key: "k", Payload: []byte(`{}`)}, false)

	rec := &recordingPublisher{}
	relay := outbox.NewRelay(pool, rec, outbox.RelayConfig{Interval: 20 * time.Millisecond, Logger: discard})
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- relay.Run(rctx) }()

	require.Eventually(t, func() bool {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&n)
		return n == 0
	}, 5*time.Second, 20*time.Millisecond)
	cancel()
	assert.NoError(t, <-done)
}
```

`TestRunDrainsUntilCancelled` observes progress only via the DB, so there is no data race with the relay goroutine.

- [x] **Step 3: Run to verify failure** — `cd pkg && go test ./outbox/` → FAIL, `undefined: outbox.SchemaSQL`.

- [x] **Step 4: Implement `pkg/outbox/outbox.go`**

```go
// Package outbox implements the transactional outbox: Write inserts a message
// in the caller's transaction; Relay publishes committed messages to Kafka.
package outbox

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// SchemaSQL creates the outbox table and index.
//
//go:embed schema.sql
var SchemaSQL string

// Message is an event waiting to be published.
type Message struct {
	Topic   string
	Key     string
	Payload []byte // JSON
	Headers map[string]string
}

// Execer is satisfied by pgx.Tx (and pgxpool.Pool, though Write is meant to
// run inside the business transaction).
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Write stores m in the outbox using tx. The trace context of ctx is saved in
// the headers so the published message continues the originating trace.
func Write(ctx context.Context, tx Execer, m Message) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("outbox id: %w", err)
	}
	h := maps.Clone(m.Headers)
	if h == nil {
		h = map[string]string{}
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(h))
	headers, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("outbox headers: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (id, topic, msg_key, payload, headers) VALUES ($1, $2, $3, $4, $5)`,
		id, m.Topic, m.Key, string(m.Payload), string(headers))
	if err != nil {
		return fmt.Errorf("outbox insert: %w", err)
	}
	return nil
}
```

- [x] **Step 5: Implement `pkg/outbox/relay.go`**

```go
package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
)

// Publisher sends messages; *kafkax.Producer implements it.
type Publisher interface {
	Publish(ctx context.Context, msgs ...kafkax.Message) error
}

// RelayConfig tunes the relay. Zero values use the defaults (100, 500ms).
type RelayConfig struct {
	BatchSize int
	Interval  time.Duration
	Logger    *slog.Logger
}

// Relay moves committed outbox rows to Kafka. Several replicas may run at
// once: rows are claimed with FOR UPDATE SKIP LOCKED. Delivery is
// at-least-once (a crash after publishing but before commit re-sends).
type Relay struct {
	pool *pgxpool.Pool
	pub  Publisher
	cfg  RelayConfig
}

func NewRelay(pool *pgxpool.Pool, pub Publisher, cfg RelayConfig) *Relay {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 500 * time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Relay{pool: pool, pub: pub, cfg: cfg}
}

// Run processes batches until ctx is cancelled. A full batch is followed
// immediately by another; otherwise it waits Interval.
func (r *Relay) Run(ctx context.Context) error {
	for {
		n, err := r.ProcessBatch(ctx)
		if err != nil && ctx.Err() == nil {
			r.cfg.Logger.ErrorContext(ctx, "outbox relay batch failed", "error", err)
		}
		if n == r.cfg.BatchSize {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(r.cfg.Interval):
		}
	}
}

// ProcessBatch publishes up to BatchSize unpublished rows in creation order
// and marks them published, all in one transaction. It returns how many rows
// were published. On publish failure nothing is marked.
func (r *Relay) ProcessBatch(ctx context.Context) (int, error) {
	var published int
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, topic, msg_key, payload, headers
			FROM outbox
			WHERE published_at IS NULL
			ORDER BY created_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED`, r.cfg.BatchSize)
		if err != nil {
			return fmt.Errorf("select outbox: %w", err)
		}
		var ids [][16]byte
		var msgs []kafkax.Message
		for rows.Next() {
			var (
				id      [16]byte
				m       kafkax.Message
				key     string
				payload []byte
			)
			if err := rows.Scan(&id, &m.Topic, &key, &payload, &m.Headers); err != nil {
				rows.Close()
				return fmt.Errorf("scan outbox: %w", err)
			}
			m.Key, m.Value = []byte(key), payload
			ids = append(ids, id)
			msgs = append(msgs, m)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate outbox: %w", err)
		}
		if len(msgs) == 0 {
			return nil
		}
		if err := r.pub.Publish(ctx, msgs...); err != nil {
			return fmt.Errorf("publish outbox: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids); err != nil {
			return fmt.Errorf("mark outbox published: %w", err)
		}
		published = len(msgs)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return published, nil
}
```

- [x] **Step 6: Run tests** — `cd pkg && go test -race ./outbox/` → PASS.

- [x] **Step 7: Commit**

```bash
git add pkg
git commit -m "feat(pkg/outbox): add transactional outbox writer and Kafka relay"
```

---

### Task 13: gRPC contract — `proto/` + `gen/`

**Files:**
- Create: `proto/buf.yaml`, `proto/buf.gen.yaml`, `proto/identity/v1/identity.proto`, `proto/catalog/v1/catalog.proto`, generated `gen/identity/v1/*.go`, `gen/catalog/v1/*.go`, `gen/gen_test.go`
- Modify: `gen/go.mod` (deps via tidy)

**Interfaces:**
- Produces: Go packages `github.com/ngoxuanchien/e-commerce/gen/identity/v1` (`identityv1`: `IdentityInternalServer`, `IdentityInternalClient`, `NewIdentityInternalClient`, `RegisterIdentityInternalServer`, `UnimplementedIdentityInternalServer`, `GrantRoleRequest`, `GrantRoleResponse`) and `.../gen/catalog/v1` (`catalogv1`: `CatalogInternalServer/Client`, `GetSkusRequest/Response`, `SkuInfo`, `StockItem`, `ReserveStockRequest/Response`, `ReleaseStockRequest/Response`, `InsufficientStock`, `StockShortage`).

- [x] **Step 1: Write the failing compile test** — `gen/gen_test.go`

```go
package gen_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	catalogv1 "github.com/ngoxuanchien/e-commerce/gen/catalog/v1"
	identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"
)

type catalogStub struct{ catalogv1.UnimplementedCatalogInternalServer }

func (catalogStub) ReserveStock(context.Context, *catalogv1.ReserveStockRequest) (*catalogv1.ReserveStockResponse, error) {
	st, _ := status.New(codes.FailedPrecondition, "insufficient stock").WithDetails(&catalogv1.InsufficientStock{
		Shortages: []*catalogv1.StockShortage{{SkuId: "s1", Requested: 2, Available: 1}},
	})
	return nil, st.Err()
}

// TestGeneratedStubsRoundTrip proves the generated code compiles and that the
// InsufficientStock detail survives the wire.
func TestGeneratedStubsRoundTrip(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	catalogv1.RegisterCatalogInternalServer(srv, catalogStub{})
	identityv1.RegisterIdentityInternalServer(srv, identityv1.UnimplementedIdentityInternalServer{})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = catalogv1.NewCatalogInternalClient(conn).ReserveStock(context.Background(), &catalogv1.ReserveStockRequest{
		OrderId: "o1", Items: []*catalogv1.StockItem{{SkuId: "s1", Quantity: 2}},
	})
	st := status.Convert(err)
	if st.Code() != codes.FailedPrecondition || len(st.Details()) != 1 {
		t.Fatalf("got %v", st)
	}
	d, ok := st.Details()[0].(*catalogv1.InsufficientStock)
	if !ok || d.GetShortages()[0].GetAvailable() != 1 {
		t.Fatalf("bad detail %#v", st.Details()[0])
	}

	_, err = identityv1.NewIdentityInternalClient(conn).GrantRole(context.Background(),
		&identityv1.GrantRoleRequest{UserId: "u1", Role: "seller"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("want Unimplemented, got %v", err)
	}
}
```

- [x] **Step 2: Run to verify failure** — `cd gen && go test ./...` → FAIL, cannot find package `.../gen/catalog/v1`.

- [x] **Step 3: Write `proto/buf.yaml` and `proto/buf.gen.yaml`**

```yaml
# proto/buf.yaml
version: v2
lint:
  use:
    - STANDARD
  except:
    # Service names are fixed by the WS0 contract (IdentityInternal, CatalogInternal).
    - SERVICE_SUFFIX
breaking:
  use:
    - FILE
```

```yaml
# proto/buf.gen.yaml — run from the repo root: buf generate proto --template proto/buf.gen.yaml
version: v2
plugins:
  - local: protoc-gen-go
    out: gen
    opt: paths=source_relative
  - local: protoc-gen-go-grpc
    out: gen
    opt: paths=source_relative
```

- [x] **Step 4: Write `proto/identity/v1/identity.proto`**

```proto
syntax = "proto3";

package identity.v1;

option go_package = "github.com/ngoxuanchien/e-commerce/gen/identity/v1;identityv1";

// IdentityInternal is identity-svc's service-to-service API (not exposed publicly).
service IdentityInternal {
  // GrantRole adds a role to a user. Idempotent: granting an existing role succeeds.
  // Errors: NOT_FOUND (unknown user), INVALID_ARGUMENT (role other than "seller").
  rpc GrantRole(GrantRoleRequest) returns (GrantRoleResponse);
}

message GrantRoleRequest {
  string user_id = 1;
  // Only "seller" is accepted in P1.
  string role = 2;
}

message GrantRoleResponse {}
```

- [x] **Step 5: Write `proto/catalog/v1/catalog.proto`**

```proto
syntax = "proto3";

package catalog.v1;

option go_package = "github.com/ngoxuanchien/e-commerce/gen/catalog/v1;catalogv1";

// CatalogInternal is catalog-svc's service-to-service API (not exposed publicly).
service CatalogInternal {
  // GetSkus returns current SKU data. Unknown SKUs are omitted. Max 100 ids.
  rpc GetSkus(GetSkusRequest) returns (GetSkusResponse);
  // ReserveStock atomically reserves stock for an order. Idempotent per order_id.
  // Insufficient stock: FAILED_PRECONDITION with an InsufficientStock detail.
  rpc ReserveStock(ReserveStockRequest) returns (ReserveStockResponse);
  // ReleaseStock returns an order's reserved stock. Idempotent; no-op if nothing is reserved.
  rpc ReleaseStock(ReleaseStockRequest) returns (ReleaseStockResponse);
}

message GetSkusRequest {
  repeated string sku_ids = 1;
}

message GetSkusResponse {
  repeated SkuInfo skus = 1;
}

message SkuInfo {
  string sku_id = 1;
  string product_id = 2;
  string product_name = 3;
  string variant_name = 4;
  string image_url = 5;
  // VND.
  int64 price = 6;
  int32 stock = 7;
  // sku.active && product.status == active.
  bool available = 8;
  string shop_id = 9;
  string shop_name = 10;
  string seller_user_id = 11;
}

message StockItem {
  string sku_id = 1;
  int32 quantity = 2;
}

message ReserveStockRequest {
  string order_id = 1;
  repeated StockItem items = 2;
}

message ReserveStockResponse {}

message ReleaseStockRequest {
  string order_id = 1;
}

message ReleaseStockResponse {}

// InsufficientStock is attached as a status detail to FAILED_PRECONDITION
// errors from ReserveStock.
message InsufficientStock {
  repeated StockShortage shortages = 1;
}

message StockShortage {
  string sku_id = 1;
  int32 requested = 2;
  int32 available = 3;
}
```

- [x] **Step 6: Generate, lint, test**

Run: `make gen-proto && make lint-proto && (cd gen && go test ./...)`
Expected: `gen/identity/v1/identity.pb.go`, `identity_grpc.pb.go`, `gen/catalog/v1/catalog.pb.go`, `catalog_grpc.pb.go` exist; `buf lint` and `buf format` print nothing; test PASS.

- [x] **Step 7: Commit**

```bash
git add proto gen
git commit -m "feat(contracts): add identity and catalog gRPC contracts with generated code"
```

---

### Task 14: OpenAPI — `common.yaml`, `identity.yaml`, redocly config, contract test helper

**Files:**
- Create: `api/redocly.yaml`, `api/openapi/common.yaml`, `api/openapi/identity.yaml`, `pkg/testutil/openapi.go`, `pkg/testutil/openapi_test.go`

**Interfaces:**
- Produces: `testutil.LoadOpenAPI(t testing.TB, path string) *openapi3.T`; `testutil.ContractTransport(t testing.TB, specPath string, base http.RoundTripper) http.RoundTripper` — `RoundTrip` returns an error when the request or response violates the spec. Shared component names in `common.yaml`: schemas `Id`, `Timestamp`, `Money`, `ErrorCode`, `Error`, `ShopRef`; parameters `Limit`, `Cursor`; responses `ValidationError`, `Unauthenticated`, `Forbidden`, `NotFound`, `Conflict`, `RateLimited`, `ServerError`; security schemes `bearerAuth`, `refreshCookie`.

- [x] **Step 1: Add dependency** — `cd pkg && go get github.com/getkin/kin-openapi@v0.149.0`

- [x] **Step 2: Write failing test** — `pkg/testutil/openapi_test.go`

```go
package testutil_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

const specDir = "../../api/openapi"

// TestContractsLoad proves every service spec is valid for kin-openapi (the
// validator WS1–WS3 use), including cross-file $refs to common.yaml.
func TestContractsLoad(t *testing.T) {
	for _, name := range []string{"identity.yaml", "catalog.yaml", "order.yaml"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(specDir, name)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				t.Skipf("%s not written yet", name)
			}
			doc := testutil.LoadOpenAPI(t, path)
			assert.NotEmpty(t, doc.Paths.Map())
		})
	}
}

func TestContractTransport(t *testing.T) {
	var respBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
	defer srv.Close()
	client := &http.Client{Transport: testutil.ContractTransport(t, filepath.Join(specDir, "identity.yaml"), nil)}

	get := func() error {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/users/me", nil)
		req.Header.Set("Authorization", "Bearer x")
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}

	respBody = `{"id":"0192a1b2-0000-7000-8000-000000000001","email":"a@example.test","full_name":"A","roles":["buyer"],"created_at":"2026-09-28T10:00:00Z"}`
	assert.NoError(t, get())

	respBody = `{"id":"not-a-uuid"}`
	err := get()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response violates contract")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/register", strings.NewReader(`{"email":"a@example.test"}`))
	req.Header.Set("Content-Type", "application/json")
	_, err = client.Do(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request violates contract")
}
```

- [x] **Step 3: Run to verify failure** — `cd pkg && go test -short ./testutil/` → FAIL, `undefined: testutil.LoadOpenAPI`.

- [x] **Step 4: Implement `pkg/testutil/openapi.go`**

```go
package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// LoadOpenAPI loads and validates an OpenAPI document (external $refs allowed).
func LoadOpenAPI(t testing.TB, path string) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	doc, err := loader.LoadFromFile(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("invalid %s: %v", path, err)
	}
	return doc
}

type contractTransport struct {
	base   http.RoundTripper
	router routers.Router
}

// ContractTransport returns a RoundTripper that validates every request and
// response against the spec at specPath. Violations are returned as errors
// from RoundTrip. Security requirements are not checked (the gateway owns auth).
func ContractTransport(t testing.TB, specPath string, base http.RoundTripper) http.RoundTripper {
	t.Helper()
	doc := LoadOpenAPI(t, specPath)
	doc.Servers = nil // match any host, e.g. httptest servers
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("openapi router: %v", err)
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &contractTransport{base: base, router: router}
}

func (c *contractTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	route, params, err := c.router.FindRoute(req)
	if err != nil {
		return nil, fmt.Errorf("request violates contract: %s %s: %w", req.Method, req.URL.Path, err)
	}

	var reqBody []byte
	if req.Body != nil {
		if reqBody, err = io.ReadAll(req.Body); err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	in := &openapi3filter.RequestValidationInput{
		Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	if err := openapi3filter.ValidateRequest(context.Background(), in); err != nil {
		return nil, fmt.Errorf("request violates contract: %s %s: %w", req.Method, req.URL.Path, err)
	}
	req.Body = io.NopCloser(bytes.NewReader(reqBody))

	resp, err := c.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	respBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	out := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in, Status: resp.StatusCode, Header: resp.Header,
		Body: io.NopCloser(bytes.NewReader(respBody)),
	}
	if err := openapi3filter.ValidateResponse(context.Background(), out); err != nil {
		return nil, fmt.Errorf("response violates contract: %s %s -> %d: %w", req.Method, req.URL.Path, resp.StatusCode, err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	return resp, nil
}
```

- [x] **Step 5: Write `api/redocly.yaml`**

```yaml
extends:
  - recommended
rules:
  # Portfolio project without a published license.
  info-license: off
  info-license-strict: off
```

- [x] **Step 6: Write `api/openapi/common.yaml`**

```yaml
openapi: 3.1.0
info:
  title: Marketplace — shared components
  version: 1.0.0
  description: Schemas, parameters, responses and security schemes shared by identity.yaml, catalog.yaml and order.yaml.
servers:
  - url: http://localhost:8080
paths: {}
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
      description: RS256 access token issued by identity-svc. Verified by the gateway.
    refreshCookie:
      type: apiKey
      in: cookie
      name: refresh_token
      description: HttpOnly refresh token cookie (Path=/api/auth).
  parameters:
    Limit:
      name: limit
      in: query
      description: Page size.
      schema:
        type: integer
        minimum: 1
        maximum: 100
        default: 20
    Cursor:
      name: cursor
      in: query
      description: Opaque cursor from a previous page's next_cursor.
      schema:
        type: string
        maxLength: 500
  schemas:
    Id:
      type: string
      format: uuid
      description: UUIDv7.
    Timestamp:
      type: string
      format: date-time
      description: UTC, RFC 3339.
    Money:
      type: integer
      format: int64
      minimum: 0
      description: Amount in VND (no decimals).
    ErrorCode:
      type: string
      enum:
        - VALIDATION_ERROR
        - UNAUTHENTICATED
        - FORBIDDEN
        - NOT_FOUND
        - CONFLICT
        - OUT_OF_STOCK
        - INVALID_STATE_TRANSITION
        - IDEMPOTENCY_KEY_REUSED
        - RATE_LIMITED
        - INTERNAL
        - UPSTREAM_UNAVAILABLE
    Error:
      type: object
      required: [error]
      properties:
        error:
          type: object
          required: [code, message]
          properties:
            code:
              $ref: '#/components/schemas/ErrorCode'
            message:
              type: string
            details:
              type: object
              additionalProperties: true
              description: |
                VALIDATION_ERROR: {"fields": {"<json path>": "<message>"}}.
                OUT_OF_STOCK: {"shortages": [{"sku_id", "requested", "available"}]}.
    ShopRef:
      type: object
      required: [id, name, slug]
      properties:
        id:
          $ref: '#/components/schemas/Id'
        name:
          type: string
        slug:
          type: string
  responses:
    ValidationError:
      description: Request validation failed (VALIDATION_ERROR).
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/Error'
    Unauthenticated:
      description: Missing or invalid credentials (UNAUTHENTICATED).
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/Error'
    Forbidden:
      description: Authenticated but not allowed (FORBIDDEN).
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/Error'
    NotFound:
      description: Resource not found or not visible to the caller (NOT_FOUND).
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/Error'
    Conflict:
      description: Conflict with current state (CONFLICT, OUT_OF_STOCK, INVALID_STATE_TRANSITION).
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/Error'
    RateLimited:
      description: Too many requests (RATE_LIMITED).
      headers:
        Retry-After:
          description: Seconds to wait before retrying.
          schema:
            type: integer
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/Error'
    ServerError:
      description: INTERNAL (500) or UPSTREAM_UNAVAILABLE (503).
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/Error'
```

- [x] **Step 7: Write `api/openapi/identity.yaml`**

```yaml
openapi: 3.1.0
info:
  title: Marketplace — identity API
  version: 1.0.0
  description: Accounts, sessions and JWKS. Public paths are served through the gateway.
servers:
  - url: http://localhost:8080
tags:
  - name: auth
    description: Registration and sessions.
  - name: users
    description: Current user.
  - name: internal
    description: Not routed by the gateway.
paths:
  /api/auth/register:
    post:
      tags: [auth]
      operationId: register
      summary: Create an account (role buyer). Does not log in.
      security: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/RegisterRequest'
      responses:
        '201':
          description: Created.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/User'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '409':
          $ref: 'common.yaml#/components/responses/Conflict'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/auth/login:
    post:
      tags: [auth]
      operationId: login
      summary: Log in; sets the refresh_token cookie.
      security: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/LoginRequest'
      responses:
        '200':
          description: Logged in.
          headers:
            Set-Cookie:
              $ref: '#/components/headers/SetRefreshCookie'
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/TokenResponse'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/auth/refresh:
    post:
      tags: [auth]
      operationId: refresh
      summary: Rotate the refresh token and issue a new access token (roles re-read).
      security:
        - refreshCookie: []
      responses:
        '200':
          description: Refreshed.
          headers:
            Set-Cookie:
              $ref: '#/components/headers/SetRefreshCookie'
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/TokenResponse'
        '401':
          description: Missing, expired, unknown or reused token; the cookie is cleared.
          headers:
            Set-Cookie:
              $ref: '#/components/headers/ClearRefreshCookie'
          content:
            application/json:
              schema:
                $ref: 'common.yaml#/components/schemas/Error'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/auth/logout:
    post:
      tags: [auth]
      operationId: logout
      summary: Revoke the current refresh token (if any) and clear the cookie. Always 204.
      security:
        - {}
        - refreshCookie: []
      responses:
        '204':
          description: Logged out.
          headers:
            Set-Cookie:
              $ref: '#/components/headers/ClearRefreshCookie'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/users/me:
    get:
      tags: [users]
      operationId: getMe
      summary: Current user with roles read from the database.
      security:
        - bearerAuth: []
      responses:
        '200':
          description: The caller.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/User'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /.well-known/jwks.json:
    get:
      tags: [internal]
      operationId: getJwks
      summary: Public signing keys (internal network only; used by the gateway).
      security: []
      x-internal: true
      responses:
        '200':
          description: JSON Web Key Set.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Jwks'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
components:
  headers:
    SetRefreshCookie:
      description: 'refresh_token=<opaque>; HttpOnly; Secure; SameSite=Lax; Path=/api/auth; Max-Age=2592000'
      schema:
        type: string
    ClearRefreshCookie:
      description: 'refresh_token=; HttpOnly; Secure; SameSite=Lax; Path=/api/auth; Max-Age=0'
      schema:
        type: string
  schemas:
    Role:
      type: string
      enum: [buyer, seller, admin]
    User:
      type: object
      required: [id, email, full_name, roles, created_at]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        email:
          type: string
          format: email
        full_name:
          type: string
        roles:
          type: array
          items:
            $ref: '#/components/schemas/Role'
        created_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
    RegisterRequest:
      type: object
      required: [email, password, full_name]
      properties:
        email:
          type: string
          format: email
          maxLength: 254
          description: Trimmed and lowercased by the server.
        password:
          type: string
          minLength: 8
          maxLength: 72
        full_name:
          type: string
          minLength: 1
          maxLength: 100
    LoginRequest:
      type: object
      required: [email, password]
      properties:
        email:
          type: string
          minLength: 1
        password:
          type: string
          minLength: 1
    TokenResponse:
      type: object
      required: [access_token, token_type, expires_in, user]
      properties:
        access_token:
          type: string
        token_type:
          type: string
          const: Bearer
        expires_in:
          type: integer
          description: Access token lifetime in seconds.
        user:
          $ref: '#/components/schemas/User'
    Jwks:
      type: object
      required: [keys]
      properties:
        keys:
          type: array
          items:
            type: object
            required: [kty, kid, use, alg, n, e]
            properties:
              kty:
                type: string
                const: RSA
              kid:
                type: string
              use:
                type: string
                const: sig
              alg:
                type: string
                const: RS256
              n:
                type: string
              e:
                type: string
```

- [x] **Step 8: Lint and test**

Run: `npx --yes @redocly/cli@2.54.3 lint --config api/redocly.yaml api/openapi/common.yaml api/openapi/identity.yaml && (cd pkg && go test -short ./testutil/)`
Expected: redocly reports both files valid; Go tests PASS (catalog/order skip).
If redocly reports a rule violation, fix the YAML rather than disabling the rule; any rule that must be turned off is recorded in the spec decisions (Task 19).

- [x] **Step 9: Commit**

```bash
git add api pkg
git commit -m "feat(contracts): add common and identity OpenAPI specs with contract test helper"
```

---

### Task 15: OpenAPI — `catalog.yaml` and `order.yaml`

**Files:**
- Create: `api/openapi/catalog.yaml`, `api/openapi/order.yaml`

**Interfaces:**
- Consumes: `common.yaml` components from Task 14.
- Produces: component schema names WS2/WS3/WS5 rely on — catalog: `Category`, `Shop`, `ShopCreate`, `ShopUpdate`, `Sku`, `SkuInput`, `ProductImage`, `Product`, `ProductSummary`, `ProductPage`, `ProductSummaryPage`, `ProductStatus`, `ProductPatch`, `ProductInput`, `UploadRequest`, `UploadResponse`; order: `Cart`, `CartShop`, `CartItem`, `CartItemUpdate`, `Address`, `CheckoutRequest`, `CheckoutResponse`, `OrderStatus`, `ActorRole`, `OrderShop`, `OrderItem`, `OrderHistoryEntry`, `Order`, `OrderPage`, `CancelRequest`, `OrderActionRequest`.

- [x] **Step 1: Confirm the contract test currently skips them** — `cd pkg && go test -short -run TestContractsLoad -v ./testutil/` → `catalog.yaml not written yet` / `order.yaml not written yet` (SKIP).

- [x] **Step 2: Write `api/openapi/catalog.yaml`**

```yaml
openapi: 3.1.0
info:
  title: Marketplace — catalog API
  version: 1.0.0
  description: Shops, categories, products, SKUs, search and image uploads.
servers:
  - url: http://localhost:8080
tags:
  - name: browse
    description: Public catalog.
  - name: shops
    description: Shops.
  - name: seller
    description: Seller center (requires role seller).
paths:
  /api/categories:
    get:
      tags: [browse]
      operationId: listCategories
      summary: Two-level category tree.
      security: []
      responses:
        '200':
          description: Top-level categories with children.
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Category'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/products:
    get:
      tags: [browse]
      operationId: searchProducts
      summary: Search/browse active products (Vietnamese, accent-insensitive).
      security: []
      parameters:
        - name: q
          in: query
          schema:
            type: string
            maxLength: 200
        - name: category
          in: query
          description: Category slug; includes child categories.
          schema:
            type: string
        - name: shop
          in: query
          description: Shop slug.
          schema:
            type: string
        - name: min_price
          in: query
          schema:
            $ref: 'common.yaml#/components/schemas/Money'
        - name: max_price
          in: query
          schema:
            $ref: 'common.yaml#/components/schemas/Money'
        - name: sort
          in: query
          description: Defaults to relevance when q is present, otherwise newest.
          schema:
            type: string
            enum: [relevance, newest, price_asc, price_desc]
        - $ref: 'common.yaml#/components/parameters/Limit'
        - $ref: 'common.yaml#/components/parameters/Cursor'
      responses:
        '200':
          description: A page of products.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ProductSummaryPage'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/products/{id}:
    get:
      tags: [browse]
      operationId: getProduct
      summary: Product detail. Non-active products are 404 except for their shop owner.
      security:
        - {}
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/ProductId'
      responses:
        '200':
          description: The product.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Product'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/shops:
    post:
      tags: [shops]
      operationId: createShop
      summary: Open a shop; the caller becomes a seller (call /api/auth/refresh afterwards for a token with the new role).
      security:
        - bearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ShopCreate'
      responses:
        '201':
          description: Created.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Shop'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '409':
          $ref: 'common.yaml#/components/responses/Conflict'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/shops/me:
    get:
      tags: [shops, seller]
      operationId: getMyShop
      x-required-role: seller
      security:
        - bearerAuth: []
      responses:
        '200':
          description: The caller's shop.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Shop'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
    patch:
      tags: [shops, seller]
      operationId: updateMyShop
      summary: Update name/description. The slug is immutable.
      x-required-role: seller
      security:
        - bearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ShopUpdate'
      responses:
        '200':
          description: Updated shop.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Shop'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/shops/{slug}:
    get:
      tags: [shops, browse]
      operationId: getShop
      security: []
      parameters:
        - name: slug
          in: path
          required: true
          schema:
            type: string
      responses:
        '200':
          description: The shop.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Shop'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/seller/products:
    get:
      tags: [seller]
      operationId: listSellerProducts
      x-required-role: seller
      security:
        - bearerAuth: []
      parameters:
        - name: status
          in: query
          schema:
            $ref: '#/components/schemas/ProductStatus'
        - $ref: 'common.yaml#/components/parameters/Limit'
        - $ref: 'common.yaml#/components/parameters/Cursor'
      responses:
        '200':
          description: A page of the seller's products.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ProductPage'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
    post:
      tags: [seller]
      operationId: createSellerProduct
      x-required-role: seller
      security:
        - bearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ProductInput'
      responses:
        '201':
          description: Created.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Product'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/seller/products/{id}:
    patch:
      tags: [seller]
      operationId: updateSellerProduct
      summary: Partial update. SKUs with id are updated, without id created, omitted ones deactivated.
      x-required-role: seller
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/ProductId'
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ProductPatch'
      responses:
        '200':
          description: Updated product.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Product'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
    delete:
      tags: [seller]
      operationId: archiveSellerProduct
      summary: Archive (status=archived).
      x-required-role: seller
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/ProductId'
      responses:
        '204':
          description: Archived.
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/seller/uploads:
    post:
      tags: [seller]
      operationId: createUpload
      summary: Presigned POST for one product image (max 5 MB, expires in 10 minutes).
      x-required-role: seller
      security:
        - bearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/UploadRequest'
      responses:
        '200':
          description: Upload form.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/UploadResponse'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
components:
  parameters:
    ProductId:
      name: id
      in: path
      required: true
      schema:
        $ref: 'common.yaml#/components/schemas/Id'
  schemas:
    Category:
      type: object
      required: [id, name, slug, children]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        name:
          type: string
        slug:
          type: string
        children:
          type: array
          description: Empty for second-level categories.
          items:
            $ref: '#/components/schemas/Category'
    Shop:
      type: object
      required: [id, name, slug, description, owner_user_id, created_at]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        name:
          type: string
        slug:
          type: string
        description:
          type: string
        owner_user_id:
          $ref: 'common.yaml#/components/schemas/Id'
        created_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
    ShopCreate:
      type: object
      required: [name, slug]
      properties:
        name:
          type: string
          minLength: 3
          maxLength: 80
        slug:
          type: string
          pattern: '^[a-z0-9-]{3,50}$'
          description: Must not be one of me, new, admin, seller.
        description:
          type: string
    ShopUpdate:
      type: object
      minProperties: 1
      properties:
        name:
          type: string
          minLength: 3
          maxLength: 80
        description:
          type: string
    ProductStatus:
      type: string
      enum: [draft, active, archived]
    Sku:
      type: object
      required: [id, name, price, stock, active]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        name:
          type: string
        price:
          $ref: 'common.yaml#/components/schemas/Money'
        stock:
          type: integer
          format: int32
          minimum: 0
        active:
          type: boolean
    SkuInput:
      type: object
      required: [name, price, stock]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        name:
          type: string
          minLength: 1
        price:
          type: integer
          format: int64
          minimum: 1000
          maximum: 1000000000
        stock:
          type: integer
          format: int32
          minimum: 0
          maximum: 100000
        active:
          type: boolean
          default: true
    ProductImage:
      type: object
      required: [object_key, url]
      properties:
        object_key:
          type: string
        url:
          type: string
          format: uri
    Product:
      type: object
      required: [id, shop, category_id, name, description, status, images, skus, min_price, created_at, updated_at]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        shop:
          $ref: 'common.yaml#/components/schemas/ShopRef'
        category_id:
          $ref: 'common.yaml#/components/schemas/Id'
        name:
          type: string
        description:
          type: string
        status:
          $ref: '#/components/schemas/ProductStatus'
        images:
          type: array
          items:
            $ref: '#/components/schemas/ProductImage'
        skus:
          type: array
          items:
            $ref: '#/components/schemas/Sku'
        min_price:
          description: Lowest price among active SKUs; null when none is active.
          oneOf:
            - $ref: 'common.yaml#/components/schemas/Money'
            - type: 'null'
        created_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
        updated_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
    ProductSummary:
      type: object
      required: [id, name, image_url, min_price, shop]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        name:
          type: string
        image_url:
          description: First image, or null.
          oneOf:
            - type: string
              format: uri
            - type: 'null'
        min_price:
          $ref: 'common.yaml#/components/schemas/Money'
        shop:
          $ref: 'common.yaml#/components/schemas/ShopRef'
    ProductSummaryPage:
      type: object
      required: [items, next_cursor]
      properties:
        items:
          type: array
          items:
            $ref: '#/components/schemas/ProductSummary'
        next_cursor:
          type: [string, 'null']
    ProductPage:
      type: object
      required: [items, next_cursor]
      properties:
        items:
          type: array
          items:
            $ref: '#/components/schemas/Product'
        next_cursor:
          type: [string, 'null']
    ProductPatch:
      type: object
      minProperties: 1
      properties:
        category_id:
          $ref: 'common.yaml#/components/schemas/Id'
        name:
          type: string
          minLength: 5
          maxLength: 200
        description:
          type: string
        status:
          $ref: '#/components/schemas/ProductStatus'
        image_keys:
          type: array
          maxItems: 9
          description: Object keys returned by /api/seller/uploads (prefix shops/{shop_id}/). Order = display order.
          items:
            type: string
        skus:
          type: array
          minItems: 1
          maxItems: 50
          items:
            $ref: '#/components/schemas/SkuInput'
    ProductInput:
      allOf:
        - $ref: '#/components/schemas/ProductPatch'
        - type: object
          required: [category_id, name, status, skus]
    UploadRequest:
      type: object
      required: [content_type]
      properties:
        content_type:
          type: string
          enum: [image/jpeg, image/png, image/webp]
    UploadResponse:
      type: object
      required: [upload_url, fields, object_key, public_url]
      properties:
        upload_url:
          type: string
          format: uri
          description: POST multipart/form-data here with `fields` followed by the `file` field.
        fields:
          type: object
          additionalProperties:
            type: string
        object_key:
          type: string
        public_url:
          type: string
          format: uri
```

- [x] **Step 3: Write `api/openapi/order.yaml`**

```yaml
openapi: 3.1.0
info:
  title: Marketplace — order API
  version: 1.0.0
  description: Cart, checkout (one order per shop, COD) and the order lifecycle.
servers:
  - url: http://localhost:8080
tags:
  - name: cart
    description: Shopping cart.
  - name: orders
    description: Buyer orders.
  - name: seller
    description: Seller order management (requires role seller).
paths:
  /api/cart:
    get:
      tags: [cart]
      operationId: getCart
      security:
        - bearerAuth: []
      responses:
        '200':
          description: Cart enriched with current catalog data.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Cart'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/cart/items/{sku_id}:
    parameters:
      - name: sku_id
        in: path
        required: true
        schema:
          $ref: 'common.yaml#/components/schemas/Id'
    put:
      tags: [cart]
      operationId: setCartItem
      summary: Set the quantity of a cart line (creates it if missing).
      security:
        - bearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/CartItemUpdate'
      responses:
        '200':
          description: Updated cart.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Cart'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '409':
          $ref: 'common.yaml#/components/responses/Conflict'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
    delete:
      tags: [cart]
      operationId: removeCartItem
      security:
        - bearerAuth: []
      responses:
        '200':
          description: Updated cart.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Cart'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/checkout:
    post:
      tags: [orders]
      operationId: checkout
      summary: Create one PENDING_CONFIRMATION order per shop, all-or-nothing, idempotent.
      security:
        - bearerAuth: []
      parameters:
        - name: Idempotency-Key
          in: header
          required: true
          schema:
            type: string
            minLength: 1
            maxLength: 100
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/CheckoutRequest'
      responses:
        '201':
          description: Orders created.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/CheckoutResponse'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '409':
          $ref: 'common.yaml#/components/responses/Conflict'
        '422':
          description: Idempotency-Key reused with a different body (IDEMPOTENCY_KEY_REUSED).
          content:
            application/json:
              schema:
                $ref: 'common.yaml#/components/schemas/Error'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/orders:
    get:
      tags: [orders]
      operationId: listMyOrders
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/StatusFilter'
        - $ref: 'common.yaml#/components/parameters/Limit'
        - $ref: 'common.yaml#/components/parameters/Cursor'
      responses:
        '200':
          description: The buyer's orders, newest first.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/OrderPage'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/orders/{id}:
    get:
      tags: [orders]
      operationId: getOrder
      summary: Visible to the buyer or the seller of the order.
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/OrderId'
      responses:
        '200':
          description: The order.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Order'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/orders/{id}/cancel:
    post:
      tags: [orders]
      operationId: cancelMyOrder
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/OrderId'
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/CancelRequest'
      responses:
        '200':
          $ref: '#/components/responses/OrderResponse'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '409':
          $ref: 'common.yaml#/components/responses/Conflict'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/orders/{id}/complete:
    post:
      tags: [orders]
      operationId: completeMyOrder
      summary: Buyer confirms receipt (DELIVERED → COMPLETED).
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/OrderId'
      responses:
        '200':
          $ref: '#/components/responses/OrderResponse'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '409':
          $ref: 'common.yaml#/components/responses/Conflict'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/seller/orders:
    get:
      tags: [seller]
      operationId: listSellerOrders
      x-required-role: seller
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/StatusFilter'
        - $ref: 'common.yaml#/components/parameters/Limit'
        - $ref: 'common.yaml#/components/parameters/Cursor'
      responses:
        '200':
          description: Orders of the seller's shop, newest first.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/OrderPage'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/seller/orders/{id}/confirm:
    post:
      tags: [seller]
      operationId: confirmSellerOrder
      summary: PENDING_CONFIRMATION → CONFIRMED.
      x-required-role: seller
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/OrderId'
      requestBody:
        $ref: '#/components/requestBodies/OptionalAction'
      responses:
        '200':
          $ref: '#/components/responses/OrderResponse'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '409':
          $ref: 'common.yaml#/components/responses/Conflict'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/seller/orders/{id}/ship:
    post:
      tags: [seller]
      operationId: shipSellerOrder
      summary: CONFIRMED → SHIPPING.
      x-required-role: seller
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/OrderId'
      requestBody:
        $ref: '#/components/requestBodies/OptionalAction'
      responses:
        '200':
          $ref: '#/components/responses/OrderResponse'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '409':
          $ref: 'common.yaml#/components/responses/Conflict'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/seller/orders/{id}/deliver:
    post:
      tags: [seller]
      operationId: deliverSellerOrder
      summary: SHIPPING → DELIVERED.
      x-required-role: seller
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/OrderId'
      requestBody:
        $ref: '#/components/requestBodies/OptionalAction'
      responses:
        '200':
          $ref: '#/components/responses/OrderResponse'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '409':
          $ref: 'common.yaml#/components/responses/Conflict'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/seller/orders/{id}/cancel:
    post:
      tags: [seller]
      operationId: cancelSellerOrder
      summary: PENDING_CONFIRMATION or CONFIRMED → CANCELLED.
      x-required-role: seller
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/OrderId'
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/CancelRequest'
      responses:
        '200':
          $ref: '#/components/responses/OrderResponse'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '403':
          $ref: 'common.yaml#/components/responses/Forbidden'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '409':
          $ref: 'common.yaml#/components/responses/Conflict'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
components:
  parameters:
    OrderId:
      name: id
      in: path
      required: true
      schema:
        $ref: 'common.yaml#/components/schemas/Id'
    StatusFilter:
      name: status
      in: query
      schema:
        $ref: '#/components/schemas/OrderStatus'
  requestBodies:
    OptionalAction:
      required: false
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/OrderActionRequest'
  responses:
    OrderResponse:
      description: The order after the transition.
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/Order'
  schemas:
    CartItem:
      type: object
      required: [sku_id, product_id, product_name, variant_name, image_url, unit_price, quantity, stock, available]
      properties:
        sku_id:
          $ref: 'common.yaml#/components/schemas/Id'
        product_id:
          $ref: 'common.yaml#/components/schemas/Id'
        product_name:
          type: string
        variant_name:
          type: string
        image_url:
          type: [string, 'null']
        unit_price:
          $ref: 'common.yaml#/components/schemas/Money'
        quantity:
          type: integer
          minimum: 1
          maximum: 99
        stock:
          type: integer
          minimum: 0
        available:
          type: boolean
          description: False when the SKU/product is inactive or no longer exists.
    CartShop:
      type: object
      required: [shop, items]
      properties:
        shop:
          $ref: 'common.yaml#/components/schemas/ShopRef'
        items:
          type: array
          items:
            $ref: '#/components/schemas/CartItem'
    Cart:
      type: object
      required: [shops, subtotal]
      properties:
        shops:
          type: array
          items:
            $ref: '#/components/schemas/CartShop'
        subtotal:
          allOf:
            - $ref: 'common.yaml#/components/schemas/Money'
          description: Sum over available lines only.
    CartItemUpdate:
      type: object
      required: [quantity]
      properties:
        quantity:
          type: integer
          minimum: 1
          maximum: 99
    Address:
      type: object
      required: [full_name, phone, line1, ward, district, province]
      properties:
        full_name:
          type: string
          minLength: 1
        phone:
          type: string
          minLength: 1
        line1:
          type: string
          minLength: 1
        ward:
          type: string
          minLength: 1
        district:
          type: string
          minLength: 1
        province:
          type: string
          minLength: 1
    CheckoutRequest:
      type: object
      required: [shipping_address, payment_method]
      properties:
        sku_ids:
          type: array
          minItems: 1
          description: Cart lines to buy; defaults to the whole cart.
          items:
            $ref: 'common.yaml#/components/schemas/Id'
        shipping_address:
          $ref: '#/components/schemas/Address'
        payment_method:
          type: string
          enum: [COD]
    CheckoutResponse:
      type: object
      required: [checkout_id, orders]
      properties:
        checkout_id:
          $ref: 'common.yaml#/components/schemas/Id'
        orders:
          type: array
          items:
            $ref: '#/components/schemas/Order'
    OrderStatus:
      type: string
      enum: [PENDING, PENDING_CONFIRMATION, CONFIRMED, SHIPPING, DELIVERED, COMPLETED, CANCELLED]
    ActorRole:
      type: string
      enum: [buyer, seller, system]
    OrderShop:
      type: object
      required: [id, name]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        name:
          type: string
    OrderItem:
      type: object
      required: [sku_id, product_id, product_name, variant_name, image_url, unit_price, quantity, line_total]
      properties:
        sku_id:
          $ref: 'common.yaml#/components/schemas/Id'
        product_id:
          $ref: 'common.yaml#/components/schemas/Id'
        product_name:
          type: string
        variant_name:
          type: string
        image_url:
          type: [string, 'null']
        unit_price:
          $ref: 'common.yaml#/components/schemas/Money'
        quantity:
          type: integer
          minimum: 1
        line_total:
          $ref: 'common.yaml#/components/schemas/Money'
    OrderHistoryEntry:
      type: object
      required: [from, to, actor_role, note, created_at]
      properties:
        from:
          description: Null for the initial PENDING entry.
          oneOf:
            - $ref: '#/components/schemas/OrderStatus'
            - type: 'null'
        to:
          $ref: '#/components/schemas/OrderStatus'
        actor_role:
          $ref: '#/components/schemas/ActorRole'
        note:
          type: [string, 'null']
        created_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
    Order:
      type: object
      required: [id, checkout_id, status, buyer_id, shop, items, subtotal, shipping_fee, total, shipping_address, payment_method, cancel_reason, history, created_at, updated_at]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        checkout_id:
          $ref: 'common.yaml#/components/schemas/Id'
        status:
          $ref: '#/components/schemas/OrderStatus'
        buyer_id:
          $ref: 'common.yaml#/components/schemas/Id'
        shop:
          $ref: '#/components/schemas/OrderShop'
        items:
          type: array
          items:
            $ref: '#/components/schemas/OrderItem'
        subtotal:
          $ref: 'common.yaml#/components/schemas/Money'
        shipping_fee:
          $ref: 'common.yaml#/components/schemas/Money'
        total:
          $ref: 'common.yaml#/components/schemas/Money'
        shipping_address:
          $ref: '#/components/schemas/Address'
        payment_method:
          type: string
          enum: [COD]
        cancel_reason:
          type: [string, 'null']
        history:
          type: array
          items:
            $ref: '#/components/schemas/OrderHistoryEntry'
        created_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
        updated_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
    OrderPage:
      type: object
      required: [items, next_cursor]
      properties:
        items:
          type: array
          items:
            $ref: '#/components/schemas/Order'
        next_cursor:
          type: [string, 'null']
    CancelRequest:
      type: object
      required: [reason]
      properties:
        reason:
          type: string
          minLength: 1
          maxLength: 200
    OrderActionRequest:
      type: object
      properties:
        reason:
          type: string
          maxLength: 200
          description: Optional note stored in the order history.
```

- [x] **Step 4: Lint and run the contract test**

Run: `make lint-openapi && (cd pkg && go test -short -run TestContractsLoad -v ./testutil/)`
Expected: redocly valid for all four files; `TestContractsLoad/catalog.yaml` and `/order.yaml` PASS (no longer skipped).

- [x] **Step 5: Commit**

```bash
git add api/openapi
git commit -m "feat(contracts): add catalog and order OpenAPI specs"
```

---

### Task 16: Service skeletons

**Files (per service `<svc>` in gateway, identity, catalog, order):**
- Create: `services/<svc>/go.mod`, `services/<svc>/cmd/server/main.go`, `services/<svc>/cmd/server/handler.go`, `services/<svc>/cmd/server/handler_test.go`, `services/<svc>/Dockerfile`, `services/<svc>/internal/domain/.gitkeep`, `services/<svc>/internal/app/.gitkeep`, `services/<svc>/internal/adapter/.gitkeep`
- Create (identity, catalog, order only): `services/<svc>/migrations/migrations.go`, `services/<svc>/migrations/000001_init.up.sql`, `services/<svc>/migrations/000001_init.down.sql`, `services/<svc>/sqlc.yaml`, `services/<svc>/internal/adapter/postgres/queries/.gitkeep`
- Modify: `go.work`

**Per-service values:**

| svc | HTTP | gRPC | Postgres | module |
|---|---|---|---|---|
| gateway | `:8080` | — | — | `github.com/ngoxuanchien/e-commerce/services/gateway` |
| identity | `:8081` | `:9081` | `identity_db` | `.../services/identity` |
| catalog | `:8082` | `:9082` | `catalog_db` | `.../services/catalog` |
| order | `:8083` | `:9083` | `order_db` | `.../services/order` |

**Interfaces:**
- Consumes: `config.Base`, `config.Load`, `logx.New`, `otelx.Setup`, `otelx.HTTPHandler`, `httpx.RequestID/Recover/AccessLog/NotFound`, `authctx.Middleware`, `server.Healthz/Readyz/Run/HealthcheckCommand`, `pgxx.Migrate/NewPool`, `grpcx.NewServer`.
- Produces: in each `cmd/server`: `newHandler(logger *slog.Logger, checks map[string]server.Check, api http.Handler) http.Handler` — health endpoints outside middleware; `api` mounted at `/` behind RequestID → Recover → AccessLog (→ authctx for non-gateway). Workstreams pass their router as `api`. `migrations.FS embed.FS`.

- [x] **Step 1: Create each `go.mod`** (identity shown; substitute the module path per table)

```
module github.com/ngoxuanchien/e-commerce/services/identity

go 1.27.1

require github.com/ngoxuanchien/e-commerce/pkg v0.0.0

replace (
	github.com/ngoxuanchien/e-commerce/gen => ../../gen
	github.com/ngoxuanchien/e-commerce/pkg => ../../pkg
)
```

- [x] **Step 2: Update `go.work`**

```
go 1.27.1

use (
	./gen
	./pkg
	./services/catalog
	./services/gateway
	./services/identity
	./services/order
)
```

- [x] **Step 3: Write failing handler tests** — `services/identity/cmd/server/handler_test.go` (identical file in catalog and order)

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func do(h http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestHealthEndpoints(t *testing.T) {
	ok := map[string]server.Check{"postgres": func(context.Context) error { return nil }}
	down := map[string]server.Check{"postgres": func(context.Context) error { return errors.New("down") }}

	assert.Equal(t, http.StatusOK, do(newHandler(discard, ok, nil), "/healthz", nil).Code)
	assert.Equal(t, http.StatusOK, do(newHandler(discard, ok, nil), "/readyz", nil).Code)
	assert.Equal(t, http.StatusServiceUnavailable, do(newHandler(discard, down, nil), "/readyz", nil).Code)
	assert.Equal(t, http.StatusOK, do(newHandler(discard, down, nil), "/healthz", nil).Code, "liveness ignores dependencies")
}

func TestUnknownRouteIsJSON404WithRequestID(t *testing.T) {
	rec := do(newHandler(discard, nil, nil), "/api/nope", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotEmpty(t, rec.Header().Get(httpx.HeaderRequestID))
	var body map[string]map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "NOT_FOUND", body["error"]["code"])
}

func TestAPIReceivesIdentityFromGatewayHeaders(t *testing.T) {
	var uid string
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { uid = authctx.UserID(r.Context()) })
	do(newHandler(discard, nil, api), "/api/x", map[string]string{authctx.HeaderUserID: "u1"})
	assert.Equal(t, "u1", uid)
}
```

Gateway — `services/gateway/cmd/server/handler_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func do(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthEndpoints(t *testing.T) {
	down := map[string]server.Check{"jwks": func(context.Context) error { return errors.New("not loaded") }}

	assert.Equal(t, http.StatusOK, do(newHandler(discard, nil, nil), "/healthz").Code)
	assert.Equal(t, http.StatusOK, do(newHandler(discard, nil, nil), "/readyz").Code)
	assert.Equal(t, http.StatusServiceUnavailable, do(newHandler(discard, down, nil), "/readyz").Code)
}

func TestUnknownRouteIsJSON404WithRequestID(t *testing.T) {
	rec := do(newHandler(discard, nil, nil), "/api/nope")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotEmpty(t, rec.Header().Get(httpx.HeaderRequestID))
	var body map[string]map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "NOT_FOUND", body["error"]["code"])
}
```

- [x] **Step 4: Run to verify failure** — `cd services/identity && go mod tidy; go test ./...` → FAIL, `undefined: newHandler` (repeat for each service).

- [x] **Step 5: Implement `handler.go`** — identity/catalog/order:

```go
package main

import (
	"log/slog"
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

// newHandler serves /healthz and /readyz directly (no access log noise from
// probes) and everything else through the standard middleware into api.
// A nil api answers 404 for every other route.
func newHandler(logger *slog.Logger, checks map[string]server.Check, api http.Handler) http.Handler {
	if api == nil {
		api = httpx.NotFound()
	}
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", server.Healthz())
	mux.Handle("GET /readyz", server.Readyz(checks))
	mux.Handle("/", httpx.RequestID(httpx.Recover(httpx.AccessLog(logger)(authctx.Middleware(api)))))
	return mux
}
```

Gateway `handler.go` (no `authctx`: the gateway produces identity headers instead of trusting them):

```go
package main

import (
	"log/slog"
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

// newHandler serves /healthz and /readyz directly and everything else through
// the standard middleware into api. A nil api answers 404 for every other route.
func newHandler(logger *slog.Logger, checks map[string]server.Check, api http.Handler) http.Handler {
	if api == nil {
		api = httpx.NotFound()
	}
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", server.Healthz())
	mux.Handle("GET /readyz", server.Readyz(checks))
	mux.Handle("/", httpx.RequestID(httpx.Recover(httpx.AccessLog(logger)(api))))
	return mux
}
```

- [x] **Step 6: Implement `main.go`** — identity (catalog/order: change the doc comment to `catalog-svc` / `order-svc` and the migrations import path to `.../services/catalog/migrations` / `.../services/order/migrations`):

```go
// Command server runs identity-svc.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/identity/migrations"
)

// Config is the service configuration; service-specific fields are added by its workstream.
type Config struct {
	config.Base
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(server.HealthcheckCommand(os.Getenv("HTTP_ADDR")))
	}
	if err := run(); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load[Config]()
	if err != nil {
		return err
	}
	logger := logx.New(os.Stdout, cfg.LogLevel).With("service", cfg.ServiceName)
	slog.SetDefault(logger)

	shutdownOtel, err := otelx.Setup(ctx, cfg.ServiceName, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownOtel(context.Background()) }()

	if err := pgxx.Migrate(cfg.DatabaseURL, migrations.FS); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	pool, err := pgxx.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	checks := map[string]server.Check{"postgres": pool.Ping}
	grpcSrv, _ := grpcx.NewServer()

	return server.Run(ctx, server.Config{
		HTTP: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           otelx.HTTPHandler(newHandler(logger, checks, nil), cfg.ServiceName),
			ReadHeaderTimeout: 10 * time.Second,
		},
		GRPC:            grpcSrv,
		GRPCAddr:        cfg.GRPCAddr,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          logger,
	})
}
```

Gateway `main.go`:

```go
// Command server runs the API gateway.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

// Config is the gateway configuration; routing/auth fields are added by WS4.
type Config struct {
	config.Base
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(server.HealthcheckCommand(os.Getenv("HTTP_ADDR")))
	}
	if err := run(); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load[Config]()
	if err != nil {
		return err
	}
	logger := logx.New(os.Stdout, cfg.LogLevel).With("service", cfg.ServiceName)
	slog.SetDefault(logger)

	shutdownOtel, err := otelx.Setup(ctx, cfg.ServiceName, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownOtel(context.Background()) }()

	return server.Run(ctx, server.Config{
		HTTP: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           otelx.HTTPHandler(newHandler(logger, nil, nil), cfg.ServiceName),
			ReadHeaderTimeout: 10 * time.Second,
		},
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          logger,
	})
}
```

- [x] **Step 7: Migrations package** (identity/catalog/order; the doc comment names the service)

`services/identity/migrations/migrations.go`:
```go
// Package migrations embeds identity-svc's SQL migrations (golang-migrate format).
package migrations

import "embed"

// FS holds every *.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
```

`000001_init.up.sql`:
```sql
-- Baseline migration created by WS0 so the migration pipeline runs end to end.
-- The owning workstream adds real schema in 000002+.
SELECT 1;
```

`000001_init.down.sql`:
```sql
SELECT 1;
```

- [x] **Step 8: `sqlc.yaml`** (identity/catalog/order) + empty `internal/adapter/postgres/queries/.gitkeep`

```yaml
version: "2"
sql:
  - engine: postgresql
    schema: migrations
    queries: internal/adapter/postgres/queries
    gen:
      go:
        package: sqlcdb
        out: internal/adapter/postgres/sqlcdb
        sql_package: pgx/v5
```

- [x] **Step 9: `Dockerfile`** (identity shown; replace `identity` with the service directory)

```dockerfile
# syntax=docker/dockerfile:1
# Build context: repository root.
FROM golang:1.27.1-bookworm AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOWORK=off
COPY pkg/go.mod pkg/go.sum pkg/
COPY gen/go.mod gen/go.sum gen/
COPY services/identity/go.mod services/identity/go.sum services/identity/
RUN --mount=type=cache,target=/go/pkg/mod cd services/identity && go mod download
COPY pkg pkg
COPY gen gen
COPY services/identity services/identity
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd services/identity && go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
USER nonroot:nonroot
ENTRYPOINT ["/server"]
```

- [x] **Step 10: Create `.gitkeep` files** for `internal/domain`, `internal/app`, `internal/adapter` in all four services.

- [x] **Step 11: Tidy, test, lint**

Run: `make tidy && for s in gateway identity catalog order; do (cd services/$s && go test ./... && go vet ./...) || break; done && make lint-go`
Expected: all PASS; golangci-lint reports `0 issues` per module.

- [x] **Step 12: Build images**

Run: `for s in gateway identity catalog order; do docker build -f services/$s/Dockerfile -t marketplace/$s:dev . || break; done`
Expected: four images built.

- [x] **Step 13: Commit**

```bash
git add go.work go.work.sum services
git commit -m "feat(services): add gateway, identity, catalog and order skeletons"
```

---

### Task 17: Local infrastructure — Docker Compose

**Files:**
- Create: `deploy/docker-compose.yml`, `deploy/docker-compose.web.yml`, `deploy/postgres/init.sql`

**Interfaces:**
- Produces: compose service names `postgres`, `redis`, `kafka`, `kafka-init`, `minio`, `minio-init`, `jaeger`, `gateway`, `identity`, `catalog`, `order`, `web`; host ports 8080 (gateway), 3000 (web), 16686 (Jaeger UI), 9000/9001 (MinIO); secret mount `/run/secrets/jwt_private.pem` in identity. Contract for WS5: `web/Dockerfile` must serve the SPA on port 80 and proxy `/api` to `http://gateway:8080`.

- [ ] **Step 1: Write `deploy/postgres/init.sql`**

```sql
-- One Postgres instance, one database per service (no cross-service access).
CREATE DATABASE identity_db;
CREATE DATABASE catalog_db;
CREATE DATABASE order_db;
```

- [ ] **Step 2: Write `deploy/docker-compose.yml`**

```yaml
name: marketplace

x-go-service: &go-service
  profiles: [app]
  restart: unless-stopped
  healthcheck:
    test: ["CMD", "/server", "healthcheck"]
    interval: 5s
    timeout: 3s
    retries: 20
    start_period: 5s

x-common-env: &common-env
  LOG_LEVEL: info
  OTEL_EXPORTER_OTLP_ENDPOINT: http://jaeger:4317

services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: marketplace
      POSTGRES_PASSWORD: marketplace
      POSTGRES_DB: postgres
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./postgres/init.sql:/docker-entrypoint-initdb.d/init.sql:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U marketplace -d order_db"]
      interval: 3s
      timeout: 3s
      retries: 30

  redis:
    image: redis:7-alpine
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 3s
      timeout: 3s
      retries: 30

  kafka:
    image: apache/kafka:4.3.1
    environment:
      KAFKA_NODE_ID: 1
      KAFKA_PROCESS_ROLES: broker,controller
      KAFKA_LISTENERS: PLAINTEXT://:9092,CONTROLLER://:9093
      KAFKA_ADVERTISED_LISTENERS: PLAINTEXT://kafka:9092
      KAFKA_CONTROLLER_LISTENER_NAMES: CONTROLLER
      KAFKA_LISTENER_SECURITY_PROTOCOL_MAP: CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT
      KAFKA_CONTROLLER_QUORUM_VOTERS: 1@kafka:9093
      KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR: 1
      KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR: 1
      KAFKA_TRANSACTION_STATE_LOG_MIN_ISR: 1
      KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS: 0
      KAFKA_AUTO_CREATE_TOPICS_ENABLE: "false"
    healthcheck:
      test: ["CMD-SHELL", "/opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server localhost:9092 >/dev/null 2>&1"]
      interval: 5s
      timeout: 10s
      retries: 30

  kafka-init:
    image: apache/kafka:4.3.1
    depends_on:
      kafka:
        condition: service_healthy
    entrypoint: ["/bin/sh", "-c"]
    command:
      - /opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 --create --if-not-exists --topic order.events --partitions 3 --replication-factor 1
    restart: "no"

  minio:
    image: pgsty/minio:RELEASE.2026-08-04T00-00-00Z
    command: server /data --console-address :9001
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    ports:
      - "9000:9000"
      - "9001:9001"
    volumes:
      - miniodata:/data

  minio-init:
    image: pgsty/mc:RELEASE.2026-09-16T00-00-00Z
    depends_on:
      - minio
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        until mc alias set local http://minio:9000 minioadmin minioadmin >/dev/null 2>&1; do sleep 1; done
        mc mb --ignore-existing local/product-images
        mc anonymous set download local/product-images
    restart: "no"

  jaeger:
    image: jaegertracing/jaeger:2.21.0
    ports:
      - "16686:16686"

  identity:
    <<: *go-service
    build:
      context: ..
      dockerfile: services/identity/Dockerfile
    environment:
      <<: *common-env
      SERVICE_NAME: identity
      HTTP_ADDR: ":8081"
      GRPC_ADDR: ":9081"
      DATABASE_URL: postgres://marketplace:marketplace@postgres:5432/identity_db?sslmode=disable
      JWT_PRIVATE_KEY_PATH: /run/secrets/jwt_private.pem
      ACCESS_TOKEN_TTL: 15m
      REFRESH_TOKEN_TTL: 720h
      COOKIE_SECURE: "false"
    volumes:
      - ./secrets/jwt_private.pem:/run/secrets/jwt_private.pem:ro
    depends_on:
      postgres:
        condition: service_healthy

  catalog:
    <<: *go-service
    build:
      context: ..
      dockerfile: services/catalog/Dockerfile
    environment:
      <<: *common-env
      SERVICE_NAME: catalog
      HTTP_ADDR: ":8082"
      GRPC_ADDR: ":9082"
      DATABASE_URL: postgres://marketplace:marketplace@postgres:5432/catalog_db?sslmode=disable
      KAFKA_BROKERS: kafka:9092
      IDENTITY_GRPC_ADDR: identity:9081
      MINIO_ENDPOINT: minio:9000
      MINIO_ACCESS_KEY: minioadmin
      MINIO_SECRET_KEY: minioadmin
      MINIO_BUCKET: product-images
      PUBLIC_ASSET_BASE_URL: http://localhost:9000/product-images
    depends_on:
      postgres:
        condition: service_healthy
      kafka-init:
        condition: service_completed_successfully
      minio-init:
        condition: service_completed_successfully

  order:
    <<: *go-service
    build:
      context: ..
      dockerfile: services/order/Dockerfile
    environment:
      <<: *common-env
      SERVICE_NAME: order
      HTTP_ADDR: ":8083"
      GRPC_ADDR: ":9083"
      DATABASE_URL: postgres://marketplace:marketplace@postgres:5432/order_db?sslmode=disable
      REDIS_URL: redis://redis:6379/0
      KAFKA_BROKERS: kafka:9092
      CATALOG_GRPC_ADDR: catalog:9082
      SHIPPING_FEE_FLAT: "30000"
      SWEEPER_INTERVAL: 60s
      PENDING_TIMEOUT: 2m
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      kafka-init:
        condition: service_completed_successfully

  gateway:
    <<: *go-service
    build:
      context: ..
      dockerfile: services/gateway/Dockerfile
    environment:
      <<: *common-env
      SERVICE_NAME: gateway
      HTTP_ADDR: ":8080"
      IDENTITY_URL: http://identity:8081
      CATALOG_URL: http://catalog:8082
      ORDER_URL: http://order:8083
      IDENTITY_JWKS_URL: http://identity:8081/.well-known/jwks.json
      CORS_ALLOWED_ORIGINS: http://localhost:3000,http://localhost:5173
      UPSTREAM_TIMEOUT: 10s
      TRUSTED_PROXIES: ""
    ports:
      - "8080:8080"
    depends_on:
      identity:
        condition: service_healthy
      catalog:
        condition: service_healthy
      order:
        condition: service_healthy

volumes:
  pgdata:
  miniodata:
```

- [ ] **Step 3: Write `deploy/docker-compose.web.yml`** (the Makefile includes it only when `web/package.json` exists)

```yaml
# Added by the Makefile once WS5 has created web/.
# Contract: web/Dockerfile serves the SPA on :80 and proxies /api to http://gateway:8080.
services:
  web:
    profiles: [app]
    build:
      context: ../web
    ports:
      - "3000:80"
    depends_on:
      gateway:
        condition: service_healthy
```

- [ ] **Step 4: Validate compose syntax** — `docker compose -f deploy/docker-compose.yml --profile app config --quiet` → exit 0.

- [ ] **Step 5: Bring the stack up (acceptance check)** — requires Docker.

Run: `make up-app && curl -fsS localhost:8080/readyz; echo; docker compose -f deploy/docker-compose.yml --profile app ps -a --format '{{.Service}} {{.State}} {{.Health}}'`
Expected: `{"status":"ok","checks":{}}` from the gateway; `identity`, `catalog`, `order`, `gateway` all `running healthy` (each healthcheck is `/server healthcheck` → own `/readyz`); `kafka-init`/`minio-init` `exited`.
Then: `docker compose -f deploy/docker-compose.yml exec kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --describe --topic order.events` → `PartitionCount: 3`; and `curl -fsS -o /dev/null -w '%{http_code}\n' localhost:16686` → `200`.
If `up --wait` rejects the exited one-shot init containers, record the workaround in the spec decisions (Task 19).

- [ ] **Step 6: Tear down** — `make down`.

- [ ] **Step 7: Commit**

```bash
git add deploy
git commit -m "feat(deploy): add docker compose stack for infra and service skeletons"
```

---

### Task 18: CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write `.github/workflows/ci.yml`**

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

jobs:
  go:
    name: go (${{ matrix.module }})
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        module: [pkg, gen, services/gateway, services/identity, services/catalog, services/order]
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: ${{ matrix.module }}/go.mod
          cache-dependency-path: '**/go.sum'
      - uses: actions/cache@v4
        with:
          path: bin
          key: tools-${{ runner.os }}-${{ hashFiles('Makefile') }}
      - run: make tools
      - name: lint
        working-directory: ${{ matrix.module }}
        run: $GITHUB_WORKSPACE/bin/golangci-lint run ./...
      - name: test
        working-directory: ${{ matrix.module }}
        run: go test -race -count=1 ./...

  contracts:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v6
        with:
          go-version-file: gen/go.mod
          cache-dependency-path: '**/go.sum'
      - uses: actions/setup-node@v5
        with:
          node-version: 24
      - uses: actions/cache@v4
        with:
          path: bin
          key: tools-${{ runner.os }}-${{ hashFiles('Makefile') }}
      - run: make tools
      - run: make lint-proto lint-openapi
      - name: buf breaking
        run: |
          if git cat-file -e origin/main:proto/buf.yaml 2>/dev/null; then
            bin/buf breaking proto --against ".git#ref=refs/remotes/origin/main,subdir=proto"
          else
            echo "proto/ not on main yet; skipping breaking check"
          fi
      - name: generated code is up to date
        run: |
          make gen
          git diff --exit-code -- gen services

  images:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: docker compose -f deploy/docker-compose.yml --profile app build

  web:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - id: detect
        run: |
          if [ -f web/package.json ]; then echo "present=true" >> "$GITHUB_OUTPUT"; else echo "present=false" >> "$GITHUB_OUTPUT"; fi
      - if: steps.detect.outputs.present == 'true'
        uses: actions/setup-node@v5
        with:
          node-version: 24
          cache: npm
          cache-dependency-path: web/package-lock.json
      - if: steps.detect.outputs.present == 'true'
        working-directory: web
        run: npm ci && npm run lint && npm test
      - if: steps.detect.outputs.present != 'true'
        run: echo "web/ not present yet; skipping"
```

- [ ] **Step 2: Validate the workflow** — `go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/ci.yml` → no findings.

- [ ] **Step 3: Commit**

```bash
git add .github
git commit -m "ci: add Go, contracts, images and web jobs"
```

---

### Task 19: Record spec decisions and verify acceptance criteria

**Files:**
- Modify: `docs/superpowers/specs/2026-09-28-marketplace-p1-ws0-foundation.md` (append §11)

- [ ] **Step 1: Append §11 "Decisions made during implementation"** to the WS0 spec (add any workaround discovered in Tasks 14/17 as an extra item):

```markdown
## 11. Decisions made during implementation

Gaps in §2–§9 resolved with the simplest option:

1. **Module wiring:** each service `go.mod` requires `pkg`/`gen` at `v0.0.0` with `replace` to `../../pkg` / `../../gen`, so modules also build with `GOWORK=off` (Docker, `go mod tidy`). `go.work.sum` is committed.
2. **Tools:** pinned in the `Makefile` and installed into `./bin` (`make tools`), not via `tool` directives, so tool dependencies never leak into service module graphs. golangci-lint v2; redocly via `npx @redocly/cli@<pinned>`.
3. **buf:** `buf.yaml` excludes lint rule `SERVICE_SUFFIX` because the contract fixes the names `IdentityInternal`/`CatalogInternal`. Code is generated with local plugins (`protoc-gen-go`, `protoc-gen-go-grpc`) into `gen/<svc>/v1`.
4. **Gateway** has no database: no `migrations/`, no `sqlc.yaml`, no gRPC port. Other services ship a no-op baseline migration `000001_init` so the migrate-on-startup path is exercised; `make gen` skips sqlc for services without query files.
5. **Error details:** `VALIDATION_ERROR` → `details.fields` maps JSON field paths (e.g. `skus[0].price`) to messages; `OUT_OF_STOCK` → `details.shortages[] {sku_id, requested, available}`. Request body limit 1 MB.
6. **OpenAPI:** `common.yaml` holds shared components; auth is `bearerAuth` (JWT) and `refreshCookie`; seller-only operations carry `x-required-role: seller`; JWKS is documented with `x-internal: true`. `PATCH /api/shops/me` accepts `{name?, description?}` (slug immutable). `ProductInput` = `ProductPatch` with `category_id, name, status, skus` required. `Product.min_price` is null when no SKU is active; `image_url` fields are null when a product has no image. Seller confirm/ship/deliver accept an optional `{reason}` note; cancel requires `reason` (1–200).
7. **Events:** `OrderStatusChanged.actor_id` is omitted when `actor_role` is `system`. Payload JSON Schemas live in `api/events/*.schema.json` and are enforced by `pkg/event` tests.
8. **Outbox table:** `outbox(id uuid pk, topic, msg_key, payload jsonb, headers jsonb, created_at, published_at)` with a partial index on unpublished rows; SQL exported as `outbox.SchemaSQL` for services to copy into a migration. Delivery is at-least-once; the writer's `traceparent` is stored in `headers`.
9. **Contract test helper:** `pkg/testutil.ContractTransport` (kin-openapi) validates requests/responses in WS1–WS3 integration tests.
10. **Health:** `/healthz` and `/readyz` bypass middleware; containers are distroless, so the binary implements `server healthcheck` for the compose healthcheck.
11. **Images:** MinIO no longer publishes official Docker images; compose and tests use the community builds `pgsty/minio` / `pgsty/mc` (pinned releases). Kafka tests use `confluentinc/confluent-local` (testcontainers' supported image); compose uses `apache/kafka:4.3.1`.
12. **Web in compose:** defined in `deploy/docker-compose.web.yml`, included by the Makefile only once `web/package.json` exists. WS5 must provide `web/Dockerfile` serving on :80 and proxying `/api` to `http://gateway:8080`; host port 3000.
```

- [ ] **Step 2: Verify every WS0 §10 criterion with fresh command output** (use superpowers:verification-before-completion)

| Criterion | Command | Pass condition |
|---|---|---|
| `make gen` clean; buf + redocly lint | `make gen && git status --porcelain gen services && make lint-proto lint-openapi` | no diff; both linters valid |
| `make up-app` → all four services healthy | `make up-app && docker compose -f deploy/docker-compose.yml --profile app ps --format '{{.Service}} {{.Health}}' && curl -fsS localhost:8080/readyz` | four services `healthy` (each healthcheck = own `/readyz` 200) |
| pkg tests for httpx, authctx, event, outbox | `cd pkg && go test -race -count=1 -v ./httpx/ ./authctx/ ./event/ ./outbox/ 2>&1 \| grep -E '^(--- \|ok\|FAIL)'` | all `ok`; outbox rollback/commit test PASS |
| `make lint && make test` green | `make lint && make test` | exit 0 |
| CI green | after push: `gh pr checks --watch` | all jobs green (checked in the finishing step) |
| `.gitignore` coverage | `git check-ignore -v .DS_Store deploy/secrets/x .env node_modules/x bin/x x.test` | each path matched |

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/specs/2026-09-28-marketplace-p1-ws0-foundation.md
git commit -m "docs(ws0): record implementation decisions"
```

- [ ] **Step 4: Finish the branch** with superpowers:finishing-a-development-branch.
