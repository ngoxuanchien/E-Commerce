# syntax=docker/dockerfile:1
# Build context: repository root.
FROM golang:1.27.1-bookworm AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOWORK=off
COPY pkg/go.mod pkg/go.sum pkg/
COPY gen/go.mod gen/go.sum gen/
COPY services/gateway/go.mod services/gateway/go.sum services/gateway/
RUN --mount=type=cache,target=/go/pkg/mod cd services/gateway && go mod download
COPY pkg pkg
COPY gen gen
COPY services/gateway services/gateway
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd services/gateway && go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
USER nonroot:nonroot
ENTRYPOINT ["/server"]
