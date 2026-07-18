# cic-module-oracle-cloud

A CIC provisioner module targeting **Oracle Cloud Infrastructure (OCI)** — the
first repository in the `cic-module-<provider>` namespace. Sibling modules
(`cic-module-aws`, `cic-module-azure`, …) would follow the same shape.

Derived from the [base-repo](https://github.com/CentralInfraCore/base-repo)
`wasm/main` template: a CIC iSDK guest module — a small WASM binary that the
relay host (`CIC-Relay/core/cabinet`) loads via [wazero](https://wazero.io) and
drives through the `Call` ABI, with a cryptographically signed release pipeline.

## What it does

Given a declared desired OCI state (an intent), this module drives the OCI REST
API to reconcile it. Because a WASM guest is sandboxed — no sockets, no clock,
no ambient I/O — it does **not** call OCI directly. It reaches the network and
secrets through **host functions** exposed by the relay's capability boundary,
backed by the relay's existing Rust machinery:

| Need | Backed by (CIC-Relay) |
|---|---|
| Outbound HTTP to OCI's REST API | `http-executor` — `reqwest` with an `EgressPolicy` host allowlist |
| Request signing / secret retrieval | `vault-adapter` — Vault transit + PKI countersign, secrets as `SecretString` |

The I/O happens at the host boundary, so each call is recorded — the module's
computation stays a deterministic function of `(input intent + host responses)`,
which keeps it provable under the CIC ProofTrace model.

```
[cic-module-oracle-cloud]              [CIC-Relay]
  WASM guest (this repo)                 host functions (capability boundary)
    Call(op, auth, data) ──ABI──►          http.request ─► http-executor → OCI REST
    imports: http.request, vault.sign      vault.sign   ─► vault-adapter (transit/countersign)
    OCI provisioning logic                 responses recorded into the ProofTrace
```

## Status — scaffold

This is the template seed. What is **not** yet built:

- **The relay-side bridge.** The relay today exposes only a `git` host module to
  WASM guests (`cmd/relay/git_host_funcs.go`). The `http-executor` and
  `vault-adapter` live on the native FFI path, **not** yet as wazero host
  functions. Exposing them as host functions — following the `git` pattern — is
  the enabling work this module depends on.
- **The `imports:` contract.** `abi.schema.yaml` today describes only `exports`.
  A guest that imports host functions needs an import surface added to the
  contract.
- **The OCI provisioning logic** in `module/handlers.go` (or its Rust
  equivalent).

## Open decision — Go or Rust

The seed carries the template's Go/TinyGo scaffold, which keeps CI green and
demonstrates the `Call` ABI. The final language is undecided: the host-function
ABI is defined at the WASM import boundary (`(i32,i32,i32,i32)->i32`, JSON in
linear memory) and is language-neutral, so a Rust guest could import the same
host module. Both options stay open.

---

## Getting Started

### Prerequisites

- `docker`
- `docker-compose`
- `make`
- `git`

### Quick Start

1. **Start the Vault Signing Agent** (separate terminal):
   ```sh
   ./tools/vault-sign-agent.sh -k <key.pem> -c <cert.crt> --root-ca-file <root.pem>
   ```
2. **Initialize the environment:**
   ```sh
   make infra.deps
   make build
   make up
   make repo.init
   ```
3. **Build and test the WASM module:**
   ```sh
   make wasm.build
   make wasm.test
   ```

See the [Developer Workflow](docs/en/workflow.md) for day-to-day development.

---

## Makefile Commands

- `make wasm.build` — build `module/module.wasm` with TinyGo and compute its `buildHash`.
- `make wasm.integrity-verify` — verify the committed `module.wasm`'s sha256 matches `project.yaml`'s `metadata.buildHash` (integrity gate, no rebuild).
- `make wasm.repro-probe` — rebuild to a scratch path and *report* bit-reproducibility (non-fatal supply-chain signal, issue #2).
- `make wasm.test` — host-load `module.wasm` against the relay cabinet ABI (wazero).
- `make check` — all code-quality checks (lint, format, typecheck).
- `make golang.quality` — Go quality gate (fmt/vet/lint/vuln) for `module/`.
- `make test` — Python test suite.
- `make manifest-verify` / `make manifest-update` — verify/regenerate `MANIFEST.sha256`.
- `make verify-release` — offline release-readiness check (schema, buildHash, ABI exports, manifest, provenance).
- `make release VERSION=v1.2.3` — create a signed release.

See the [Makefile Cheatsheet](docs/en/makefile-cheatsheet.md) for the full list.

---

## Inherited: Schema Compiler & Signing Infrastructure

The release/signing pipeline (`tools/`, `mk/infra.mk`, `project.yaml` +
`project.schema.yaml`) is inherited from the CIC schema-compiler ecosystem.
Vault handles signing (private keys never leave Vault); the environment is
containerized for reproducibility. Most work on this module will not touch it
beyond `make release`.
