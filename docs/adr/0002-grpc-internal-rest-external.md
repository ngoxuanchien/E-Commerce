# ADR 0002 — gRPC internally, REST externally

- Status: Accepted
- Date: 2026-09-28

## Context

Browsers need a plain JSON API that the React app can type from a contract.
Service-to-service calls, meanwhile, are few and latency-sensitive:
- catalog → identity `GrantRole`;
- order → catalog `GetSkus`, `ReserveStock` and `ReleaseStock`.

They need strict schemas, deadlines and rich error details, such as which
SKUs are short of stock.

## Decision

- **External:** REST/JSON, described by OpenAPI 3.1 in `api/openapi/`. It's
  the source of truth: the web app generates its TypeScript types from it,
  and the Go services' integration tests (plus the e2e suite) validate real
  requests and responses against it.
- **Internal:** gRPC with protobuf in `proto/`, generated into `gen/`. `buf
  lint` and `buf breaking` run in CI. Internal RPCs are never routed by the
  gateway.
- **Gateway:** proxies REST unchanged. It doesn't translate REST to gRPC, and
  it contains no business logic.

## Consequences

- There are two contract formats to maintain, but each fits its consumer.
  Contract changes go through a dedicated PR, and breaking proto changes are
  blocked automatically.
- gRPC errors carry typed details: `FAILED_PRECONDITION` +
  `InsufficientStock{shortages}`. order-svc maps them to REST
  `409 OUT_OF_STOCK` with `details.shortages`.
- The e2e suite found that the REST contract lags the gateway in one place:
  public operations don't document the 401 for an invalid token (finding F2).
