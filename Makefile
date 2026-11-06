# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Developer conveniences. `make` on its own lists the targets. CI runs the same
# underlying commands; see docs/development.md.

GO  ?= go
PKG ?= ./...
OUT ?= out

# The version .github/workflows/ci.yml pins. `make lint` fetches it rather than
# skipping: a lint that silently does not run looks like a lint that passed.
GOLANGCI_VERSION ?= v2.15.0
SQLC_VERSION     ?= v1.31.1
OASDIFF_VERSION  ?= v1.35.0
SPECTRAL_IMAGE   ?= stoplight/spectral:6.17.0@sha256:c8ffae6ee561b70e80402f1ea669e8b37c74fee48a3dbe2147b71359ed987239
PROMETHEUS_IMAGE ?= prom/prometheus:v3.7.3@sha256:49214755b6153f90a597adcbff0252cc61069f8ab69ce8411285cd4a560e8038

# The compose.yaml database, for make run and make test-db.
DEV_DATABASE_URL ?= postgres://andante:andante@localhost:5432/andante?sslmode=disable

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

BUILDINFO := github.com/gerrowadat/andante/internal/buildinfo
LDFLAGS   := -X $(BUILDINFO).version=$(VERSION) \
             -X $(BUILDINFO).commit=$(COMMIT) \
             -X $(BUILDINFO).date=$(DATE)

.DEFAULT_GOAL := help

##@ Getting started

.PHONY: run
run: ## Run andante-server against the compose.yaml Postgres with text logs
	docker compose up -d --wait postgres
	$(GO) run -ldflags='$(LDFLAGS)' ./cmd/andante-server \
	  --database-url='$(DEV_DATABASE_URL)' --base-url=http://localhost:8080 --log-format=text

.PHONY: help
help: ## List the targets
	@awk 'BEGIN {FS = ":.*##"} \
	     /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5); next} \
	     /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' \
	     $(MAKEFILE_LIST)
	@echo

.PHONY: version
version: ## Print the version that `make build` stamps
	@echo $(VERSION)

##@ Building and checking

.PHONY: build
build: ## Build both binaries into out/, version-stamped
	$(GO) build -trimpath -ldflags='$(LDFLAGS)' -o $(OUT)/andante-server ./cmd/andante-server
	$(GO) build -trimpath -ldflags='$(LDFLAGS)' -o $(OUT)/andante ./cmd/andante

.PHONY: test
test: ## Run the tests with the race detector, as CI does
	$(GO) test -race -count=1 $(PKG)

.PHONY: e2e
e2e: ## Run the end-to-end suite, which only the release workflow runs in CI
	docker compose up -d --wait postgres
	ANDANTE_TEST_DATABASE_URL='$(DEV_DATABASE_URL)' ANDANTE_REQUIRE_DB=1 $(GO) test -tags=e2e -race -count=1 ./test/e2e/...

.PHONY: dist
dist: ## Build the release archives into out/dist, as the release workflow does
	VERSION=$(VERSION) COMMIT=$(COMMIT) ./scripts/build-dist.sh $(OUT)/dist

.PHONY: lint
lint: ## Run golangci-lint, fetching the pinned version if it is not installed
	@if command -v golangci-lint >/dev/null 2>&1; then \
	  golangci-lint run; \
	else \
	  echo "golangci-lint not on PATH; running $(GOLANGCI_VERSION) via go run"; \
	  $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run; \
	fi

.PHONY: fmt
fmt: ## Format the tree with gofmt
	gofmt -l -w .

.PHONY: test-db
test-db: ## Run the tests against the compose.yaml Postgres, with no skips
	docker compose up -d --wait postgres
	ANDANTE_TEST_DATABASE_URL='$(DEV_DATABASE_URL)' ANDANTE_REQUIRE_DB=1 $(GO) test -race -count=1 $(PKG)

.PHONY: generate
generate: ## Regenerate generated code with sqlc and oapi-codegen
	$(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate
	$(GO) tool oapi-codegen -config api/oapi-client.yaml api/openapi.yaml
	$(GO) tool oapi-codegen -config api/oapi-server.yaml api/openapi.yaml

.PHONY: spec-lint
spec-lint: ## Lint api/openapi.yaml with Spectral, in Docker as CI does
	docker run --rm -v "$(CURDIR):/work:ro" -w /work $(SPECTRAL_IMAGE) \
	  lint api/openapi.yaml --ruleset .spectral.yaml --fail-severity=warn

.PHONY: spec-diff
spec-diff: ## Compare api/openapi.yaml with BASE (default origin/main), failing on breaking changes
	BASE=$(or $(BASE),origin/main) OASDIFF_VERSION=$(OASDIFF_VERSION) ./scripts/spec-diff.sh

##@ Containers

.PHONY: docker
docker: ## Build the image for this machine's platform as andante:dev
	docker build \
	  --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) \
	  -t andante:dev .

.PHONY: docker-scan
docker-scan: docker ## Scan andante:dev as CI does (needs Trivy)
	@command -v trivy >/dev/null 2>&1 || { echo "trivy is not installed"; exit 1; }
	trivy image --severity CRITICAL,HIGH --ignore-unfixed --exit-code 1 andante:dev

##@ Deployment

# NOMAD_ADDR points nowhere on purpose. Nomad then validates the job locally, and
# this never reaches whichever cluster the shell is set up for.
.PHONY: deploy-check
deploy-check: ## Check the Nomad job and the Prometheus alerts (needs nomad and Docker)
	NOMAD_ADDR=http://127.0.0.1:1 NOMAD_TOKEN= nomad job validate deploy/nomad/andante.nomad.hcl
	docker run --rm -v "$(CURDIR)/deploy/prometheus:/work:ro" -w /work --entrypoint promtool $(PROMETHEUS_IMAGE) check rules alerts.yml
	docker run --rm -v "$(CURDIR)/deploy/prometheus:/work:ro" -w /work --entrypoint promtool $(PROMETHEUS_IMAGE) test rules alerts_test.yml

##@ Housekeeping

.PHONY: clean
clean: ## Remove out/
	rm -rf $(OUT)
