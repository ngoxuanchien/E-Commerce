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

BUF := $(BIN)/buf
SQLC := $(BIN)/sqlc
GOLANGCI_LINT := $(BIN)/golangci-lint

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
	PATH="$(BIN):$$PATH" $(BUF) generate proto --template proto/buf.gen.yaml
	cd gen && go mod tidy

gen-sqlc: tools
	@for f in services/*/sqlc.yaml; do \
		d=$$(dirname $$f); \
		if ls $$d/internal/adapter/postgres/queries/*.sql >/dev/null 2>&1; then \
			echo "sqlc generate $$d"; (cd $$d && $(SQLC) generate) || exit 1; \
		else echo "sqlc: skip $$d (no queries yet)"; fi; \
	done

lint: lint-go lint-proto lint-openapi ## Run all linters

lint-go: tools
	@for m in $(GO_MODULES); do echo "golangci-lint $$m"; (cd $$m && $(GOLANGCI_LINT) run ./...) || exit 1; done

lint-proto: tools
	$(BUF) lint proto
	$(BUF) format --diff --exit-code proto

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
