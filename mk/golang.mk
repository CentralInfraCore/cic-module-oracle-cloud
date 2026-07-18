# ---- Go quality gate for module/ (the WASM guest module) ----
#
# This file was inherited from CIC-Relay's mk/golang.mk and trimmed to what
# the WASM template actually needs: `golang.quality` (fmt-check/vet/lint/vuln
# on module/), used by the CI "Go quality gate" step and by `make check`'s
# sibling targets. The relay-specific build/release/coverage-threshold/
# canonicalize/crt_parser/mq-publish machinery was removed — it referenced
# packages (cmd/relay, tools/canonicalize, tools/certutils) and services
# (nats-cli, mod-cache-loader) that do not exist in this template.

.PHONY: golang.all golang.help golang.fmt golang.fmt-check golang.lint golang.vet golang.quality \
	golang.test golang.coverage golang.coverage-profile golang.coverage-html golang.coverage-threshold \
	golang.vuln golang.deps golang.clean golang.tdd oci.extract.test oci.generate

# Default to showing help
golang.all: golang.help

VERSION  ?= dev
COMMIT   ?= $(shell git rev-parse --short HEAD)
BUILD_DIR ?= ./output/$(COMMIT)

# ---- Coverage outputs ----
COVERAGE_FILE ?= /output/$(COMMIT)/coverage.out
COVERAGE_HTML ?= /output/$(COMMIT)/coverage.html

GOFLAGS  ?= -mod=readonly -trimpath

# Kapcsolható race detektor: dev/CI ON, release OFF (RACE=0)
RACE ?= 1
ifeq ($(RACE),1)
  GO_RACE := -race
else
  GO_RACE :=
endif

# GO_MODULE_DIR: where the Go module lives relative to /app. The wasm
# template's guest module is at module/ (module/go.mod).
GO_MODULE_DIR ?= module

define GO_EXEC
	docker compose exec -T builder sh -eu -o pipefail -c 'cd /app/$(GO_MODULE_DIR) && $(1)'
endef
define GO_FIXER
	docker compose exec -T builder sh -eu -o pipefail -c 'cd /app/$(GO_MODULE_DIR) && $(1)'
endef

# Clean output for this commit
golang.clean:
	rm -rf $(BUILD_DIR)
	@echo "Cleaned build output for commit $(COMMIT)"

# ---- Help ----
golang.help: ## Show available make targets
	@echo "Available targets:"
	@grep -E '^golang\.[a-zA-Z0-9_.-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
	awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ---- Dependency Management ----
golang.deps: ## Tidy go module files
	@echo "Tidying go module files..."
	@$(call GO_FIXER, go mod tidy)

# ---- Quality gate ----
golang.fmt: ## Apply gofmt -s (and goimports if available)
	$(call GO_FIXER, git config --global --add safe.directory /app && \
		git ls-files -z -- "*.go" | xargs -0 gofmt -s -w ; \
		if command -v goimports >/dev/null 2>&1; then \
			git ls-files -z -- "*.go" | xargs -0 goimports -w ; \
		fi )

golang.fmt-check: ## Fail if formatting differs
	$(call GO_EXEC, M="$$(git ls-files -z -- "*.go" | xargs -0 gofmt -s -l)"; \
		test -z "$$M" || { printf "%s\n" "$$M"; echo "Code not formatted. Run make golang.fmt"; exit 1; })

golang.lint: ## Run static linters (staticcheck, ineffassign)
	mkdir -p $(BUILD_DIR) && $(call GO_EXEC, \
		set -euo pipefail; \
		PKGS="$$(go list ./... | grep -v /vendor/)"; \
		if [ -z "$$PKGS" ]; then \
			echo "No Go packages to lint."; \
			exit 0; \
		fi; \
		echo "Staticcheck on: $$PKGS"; \
		GO111MODULE=on GOFLAGS="$(GOFLAGS)" staticcheck $$PKGS \
	)

golang.vet: ## Run go vet
	mkdir -p $(BUILD_DIR) && $(call GO_EXEC, \
		set -euo pipefail; \
		PKGS="$$(go list ./... | grep -v /vendor/)"; \
		if [ -z "$$PKGS" ]; then \
			echo "No Go packages to lint."; \
			exit 0; \
		fi; \
		echo "Vet on: $$PKGS"; \
		GO111MODULE=on GOFLAGS="$(GOFLAGS)" go vet $$PKGS \
	)

golang.vuln: ## Run Go vulnerability scan (govulncheck)
	mkdir -p $(BUILD_DIR) && $(call GO_EXEC, \
		set -euo pipefail; \
		PKGS="$$(go list ./... | grep -v /vendor/)"; \
		if [ -z "$$PKGS" ]; then \
			echo "No Go packages to lint."; \
			exit 0; \
		fi; \
		echo "govulncheck on: $$PKGS"; \
		GO111MODULE=on GOFLAGS="$(GOFLAGS)" govulncheck $$PKGS \
	)

golang.quality: golang.fmt-check golang.lint golang.vet golang.vuln ## Quality gate: all checks must pass

# ---- Tests & coverage ----
golang.test: ## Run unit tests (verbose, race)
	mkdir -p $(BUILD_DIR) && $(call GO_EXEC, \
		set -euo pipefail; \
		PKGS="$$(go list ./... | grep -v /vendor/)"; \
		if [ -z "$$PKGS" ]; then \
			echo "No Go packages to test."; \
			exit 0; \
		fi; \
		echo "Test on: $$PKGS"; \
		GO111MODULE=on GOFLAGS="$(GOFLAGS)" go test $(GO_RACE) -v $$PKGS \
	)

golang.coverage: golang.coverage-profile golang.coverage-html ## Run tests with coverage (profile + HTML)
	@echo "Coverage HTML: $(COVERAGE_HTML)"

golang.coverage-profile: ## Run tests with coverage (profile)
	mkdir -p $(BUILD_DIR) && $(call GO_EXEC, \
		set -euo pipefail; \
		PKGS="$$(go list ./... | grep -v /vendor/)"; \
		if [ -z "$$PKGS" ]; then \
			echo "No Go packages to test."; \
			exit 0; \
		fi; \
		GOFLAGS="$(GOFLAGS)" go test $(GO_RACE) -covermode=atomic -coverprofile=$(COVERAGE_FILE) $$PKGS \
	)

golang.coverage-html: ## Run tests with coverage (HTML)
	$(call GO_EXEC, mkdir -p $(BUILD_DIR) \
		&& go tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML))

COVERAGE_MIN ?= 85

golang.coverage-threshold: golang.coverage ## Fail if coverage < $(COVERAGE_MIN)%
	mkdir -p $(BUILD_DIR) && docker compose exec -T builder sh -c 'cd /app/$(GO_MODULE_DIR) && \
		go tool cover -func=$(COVERAGE_FILE) | \
		awk -v MIN=$(COVERAGE_MIN) '"'"'/^total:/ { gsub("%","",$$3); v=$$3+0 } END { if (v < MIN) { printf "Coverage below %d%% (got %.1f%%)\n", MIN, v; exit 1 } else { printf "Coverage OK: %.1f%% >= %d%%\n", v, MIN } }'"'"''

# ---- Optional: TDD loop ----
golang.tdd: ## TDD loop with reflex
	$(call GO_EXEC, \
		command -v reflex >/dev/null 2>&1 || go install github.com/cespare/reflex@latest; \
		reflex -r "(\.go|go\.mod|go\.sum)$$" -- sh -c "GOFLAGS=-mod=readonly\ -trimpath go test -race -count=1 ./..." \
	)

# ---- OCI schema extractor (roadmap P2.2) ----
# A separate, stdlib-only Go module (tools/oci-extract) that reads the pinned
# OCI Go SDK source with go/ast and emits the model registry. Its own module, so
# it is tested here rather than under GO_MODULE_DIR (the wasm guest).
oci.extract.test: ## Vet + test the OCI schema extractor (tools/oci-extract, P2.2)
	@echo "--- OCI schema extractor: go vet + test ---"
	@docker compose exec -T builder sh -eu -o pipefail -c \
		'cd /app/tools/oci-extract && go vet ./... && go test ./...'

# ---- Regenerate the embedded CIC payload schemas (roadmap P2.3) ----
# Downloads the pinned OCI SDK (oci-sdk.lock.yaml) into a scratch module cache
# and regenerates module/schemas/<resource>.json from it. NOT a CI gate — it
# needs network; the generated JSON is committed so the guest build is offline.
# The pin here must match oci-sdk.lock.yaml provider_dependency.version.
OCI_SDK_VERSION ?= v65.121.0
oci.generate: ## Regenerate module/schemas/*.json from the pinned OCI SDK (needs network)
	@echo "--- Regenerating embedded CIC payload schemas from OCI SDK $(OCI_SDK_VERSION) ---"
	@docker compose exec -T builder sh -eu -o pipefail -c '\
		export GOPATH=/tmp/ocigp GOMODCACHE=/tmp/ocigp/pkg/mod GOFLAGS=-mod=mod; \
		cd /tmp && go mod download github.com/oracle/oci-go-sdk/v65@$(OCI_SDK_VERSION); \
		SDK=/tmp/ocigp/pkg/mod/github.com/oracle/oci-go-sdk/v65@$(OCI_SDK_VERSION)/core; \
		cd /app/tools/oci-extract && \
		go run ./cmd/oci-extract -schema Vcn -ns cic:network:vcn \
			"$$SDK/create_vcn_details.go" "$$SDK/update_vcn_details.go" \
			"$$SDK/vcn.go" "$$SDK/change_vcn_compartment_details.go" > /app/module/schemas/vcn.json'
	@echo "OK: module/schemas/vcn.json regenerated — review the diff and commit"
