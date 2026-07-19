# ---- WASM module build & test ----
#
# TinyGo target decision (wasm-template-plan.md, sec. 2.1): TinyGo
# `-target wasip1 -scheduler=none` is the only build path proven against the
# relay host (CIC-Relay/core/cabinet/Makefile:28 uses `-target wasi`). If the
# pinned TinyGo version does not define the `wasip1` build tag, fall back to
# `-target wasi` via WASM_TARGET below and adjust module/abi.go's build tag to
# match.

.PHONY: wasm.build wasm.test wasm.buildhash wasm.integrity-verify wasm.repro-probe wasm.rebuild-verify

WASM_TARGET ?= wasip1
WASM_OUT    := module/module.wasm

# Build the guest module with TinyGo inside the builder container.
wasm.build: ## Build module/module.wasm with TinyGo (-target $(WASM_TARGET) -scheduler=none)
	@echo "--- Building WASM guest module (TinyGo -target $(WASM_TARGET)) ---"
	docker compose exec -T builder sh -eu -o pipefail -c \
		'cd /app/module && tinygo build -o module.wasm -target $(WASM_TARGET) -scheduler=none .'
	@$(MAKE) wasm.buildhash

# Compute sha256(module.wasm) and write it into project.yaml's metadata.buildHash.
# This is the WASM-specific signed-artifact delta (wasm-template-plan.md, sec. 2.2):
# the schemas release signs the *source-spec* checksum (tools/infra.py), the
# buildHash is filled here in the build-gap between prepare and finalize.
wasm.buildhash: ## Compute sha256(module.wasm) -> project.yaml metadata.buildHash
	@test -f $(WASM_OUT) || { echo "$(WASM_OUT) not found — run 'make wasm.build' first"; exit 1; }
	docker compose exec -T builder sh -eu -o pipefail -c \
		'cd /app && python -m tools.compiler set-build-hash --file $(WASM_OUT) --project project.yaml'

# Artifact integrity gate (trust model — docs/design/architecture.md,
# relay-requirements.md). The committed module.wasm is the signed, first-class
# artifact: the developer counter-signs it and CIC counter-signs the companion
# metadata, both bound by metadata.buildHash (== the binary's sha256, the anchor
# the Vault signature covers via tools/infra.py _resign_with_build_hash). So CI
# verifies INTEGRITY — the committed binary matches its signed declaration — not
# reproduction. Cross-environment bit-reproducibility is NOT required by the
# trust chain and is not achievable from TinyGo's flags alone (issue #2); the
# reproducibility signal lives in the non-fatal wasm.repro-probe below.
wasm.integrity-verify: ## Verify sha256(committed module.wasm) == project.yaml metadata.buildHash (no rebuild)
	@echo "--- Verifying WASM artifact integrity (hash == buildHash) ---"
	@test -f $(WASM_OUT) || { echo "$(WASM_OUT) not found — run 'make wasm.build' first"; exit 1; }
	docker compose exec -T builder sh -eu -o pipefail -c \
		'cd /app && ART_HASH=$$(sha256sum $(WASM_OUT) | cut -d" " -f1) && \
		EXPECTED_HASH=$$(grep -E "^[[:space:]]*buildHash:" project.yaml | sed -E "s/^[[:space:]]*buildHash:[[:space:]]*//") && \
		echo "artifact sha256:        $$ART_HASH" && \
		echo "project.yaml buildHash: $$EXPECTED_HASH" && \
		if [ "$$ART_HASH" != "$$EXPECTED_HASH" ]; then \
			echo "FATAL: committed module.wasm does not match project.yaml metadata.buildHash" >&2; \
			echo "  artifact: $$ART_HASH" >&2; \
			echo "  declared: $$EXPECTED_HASH" >&2; \
			echo "The signed artifact and its declared hash disagree. Run \"make wasm.build\" to refresh both, then commit." >&2; \
			exit 1; \
		fi; \
		echo "OK: artifact integrity verified"'

# Reproducibility probe (supply-chain hardening, NON-FATAL). Rebuild to a
# scratch path and REPORT whether this environment reproduces the committed
# artifact bit-for-bit. TinyGo currently embeds the absolute build path and
# orders some cgo symbols by filesystem order, so a rebuild in a different
# environment can differ (issue #2). This is a signal, never a gate — the
# leading '-' makes the recipe non-fatal.
wasm.repro-probe: ## Rebuild to scratch and REPORT bit-reproducibility (non-fatal, issue #2)
	@echo "--- WASM reproducibility probe (non-fatal) ---"
	@test -f $(WASM_OUT) || { echo "$(WASM_OUT) not found — run 'make wasm.build' first"; exit 1; }
	-@docker compose exec -T builder sh -eu -o pipefail -c \
		'cd /app/module && tinygo build -o /tmp/module.wasm.repro -target $(WASM_TARGET) -scheduler=none . && \
		REBUILD_HASH=$$(sha256sum /tmp/module.wasm.repro | cut -d" " -f1) && \
		rm -f /tmp/module.wasm.repro && \
		COMMITTED_HASH=$$(sha256sum /app/module/module.wasm | cut -d" " -f1) && \
		echo "committed sha256: $$COMMITTED_HASH" && \
		echo "rebuilt   sha256: $$REBUILD_HASH" && \
		if [ "$$REBUILD_HASH" = "$$COMMITTED_HASH" ]; then \
			echo "REPRODUCIBLE: this environment reproduced the artifact bit-for-bit"; \
		else \
			echo "NOT bit-reproducible in this environment (expected — see issue #2)"; \
		fi'

# Back-compat alias: the old fatal rebuild-and-compare check is superseded by
# the trust model. Kept as an alias to the integrity gate so any stale caller
# keeps working; do not add new references — use wasm.integrity-verify.
wasm.rebuild-verify: wasm.integrity-verify ## Deprecated alias for wasm.integrity-verify

# Module test suite: the wazero host-load tests (load module.wasm, verify the ABI
# and drive the ops — including execute/observe via a mock cic-flow and the real
# crypto relay-integration test) AND the host-side domain unit tests
# (provider/oci_sign/contracts). Runs every `go test` in module/ so all
# assertions are guarded in CI, not just the host-load smoke tests.
wasm.test: ## Run the module's full go test suite (host-load + domain unit + integration)
	@test -f $(WASM_OUT) || { echo "$(WASM_OUT) not found — run 'make wasm.build' first"; exit 1; }
	docker compose exec -T builder sh -eu -o pipefail -c \
		'cd /app/module && GOFLAGS=-mod=mod go test -v .'
