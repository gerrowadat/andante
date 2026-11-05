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
ACT_VERSION      ?= v0.2.97

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

BUILDINFO := github.com/gerrowadat/andante/internal/buildinfo
LDFLAGS   := -X $(BUILDINFO).version=$(VERSION) \
             -X $(BUILDINFO).commit=$(COMMIT) \
             -X $(BUILDINFO).date=$(DATE)

.DEFAULT_GOAL := help

##@ Getting started

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

.PHONY: generate
generate: ## Regenerate generated code (nothing yet; sqlc arrives in phase 3)
	@echo "nothing to generate"

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

##@ Running CI locally

# act runs .github/workflows/ci.yml in containers and needs Docker.
ACT_BIN = $(shell command -v act 2>/dev/null || echo "$(GO) run github.com/nektos/act@$(ACT_VERSION)")

# --path-format=absolute because Docker rejects a relative bind-mount path.
GIT_DIR        := $(shell git rev-parse --path-format=absolute --git-dir 2>/dev/null)
GIT_COMMON_DIR := $(shell git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)

# A linked worktree's .git is a pointer to a host path the job container cannot
# see, so mount the real one. In the primary checkout the two paths match, and
# the mount would block actions/checkout from writing .git in the container.
ifeq ($(GIT_DIR),$(GIT_COMMON_DIR))
ACT_CONTAINER_OPTIONS :=
else
ACT_CONTAINER_OPTIONS := --container-options "-v $(GIT_COMMON_DIR):$(GIT_COMMON_DIR):ro"
endif

# act's cache store is shared by every checkout on the machine, and its keys
# are the same across worktrees of a repo, so keep one store per checkout.
ACT_CACHE := $(GIT_COMMON_DIR)/act-cache/$(notdir $(CURDIR))
ACT_LOCK  := $(GIT_COMMON_DIR)/act.lock
ACT_RAW    = $(ACT_BIN) $(ACT_CONTAINER_OPTIONS) --cache-server-path "$(ACT_CACHE)"

# Containers are named after the workflow and job only, so two checkouts share
# them: the guard drops ones built for another checkout, and flock (where
# available) stops two runs driving one container at once.
ACT_FLOCK := $(shell command -v flock 2>/dev/null)
ifneq ($(ACT_FLOCK),)
ACT_FLOCK += $(ACT_LOCK)
endif

.PHONY: ci
ci: ## Run the ci workflow locally under act
	./scripts/act-guard.sh $(CURDIR) && $(ACT_FLOCK) $(ACT_RAW) -W .github/workflows/ci.yml

.PHONY: ci-clean
ci-clean: ## Remove this repo's act containers, volumes and cache
	$(ACT_FLOCK) ./scripts/act-guard.sh --clean $(CURDIR) "$(ACT_CACHE)"

##@ Housekeeping

.PHONY: clean
clean: ## Remove out/
	rm -rf $(OUT)
